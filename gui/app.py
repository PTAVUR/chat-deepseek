#!/usr/bin/env python3
"""chat-deepseek 的管理界面（PySide6）。

界面只负责调用 chat-deepseek 子进程并展示结果；参数拼装、路径和密码处理都在
CLI 侧完成。所有阻塞操作放在后台线程，主线程只处理界面状态。
"""

from __future__ import annotations

import os
import queue
import sys
import threading
import time
from pathlib import Path
from typing import Callable

GUI_DIR = Path(__file__).resolve().parent
if getattr(sys, "frozen", False):
    # PyInstaller 单文件模式把资源解压到 sys._MEIPASS；assets/ 也放在这一层。
    GUI_DIR = Path(getattr(sys, "_MEIPASS", GUI_DIR))
if str(GUI_DIR) not in sys.path:
    sys.path.insert(0, str(GUI_DIR))

try:
    from PySide6.QtCore import QRectF, Qt, QTimer, QUrl
    from PySide6.QtGui import (
        QColor,
        QDesktopServices,
        QIcon,
        QPainter,
        QPixmap,
    )
    from PySide6.QtSvg import QSvgRenderer
    from PySide6.QtWidgets import (
        QApplication,
        QFrame,
        QHBoxLayout,
        QMainWindow,
        QScrollArea,
        QSizePolicy,
        QVBoxLayout,
        QWidget,
    )
except ImportError as exc:  # pragma: no cover - 依赖缺失时给出可操作提示
    raise SystemExit(
        "需要 PySide6；请按 gui/README.md 创建 venv 并安装依赖"
    ) from exc

import cli
import dialogs
import gui_config
import parsing
import server_config
import theme
import widgets


class MainWindow(QMainWindow):
    def __init__(self) -> None:
        super().__init__()
        self.setWindowTitle("chat-deepseek")
        self.resize(1000, 760)
        self.setMinimumSize(900, 740)
        self.setWindowIcon(self._make_icon())

        binary = Path(cli.find_binary()).expanduser()
        self.defaults = server_config.defaults(binary.parent)
        self.gui_config_path = gui_config.default_path()

        initial = dict(self.defaults)
        saved = gui_config.load(self.gui_config_path)
        if not saved:
            for seed in gui_config.seed_paths():
                saved = gui_config.load(seed)
                if saved:
                    break
        for key in ("binary", "data_dir", "backup_dir"):
            if saved.get(key):
                initial[key] = saved[key]
        saved_binary = Path(initial["binary"]).expanduser()
        if not saved_binary.is_file() and not cli.binary_in(saved_binary).is_file():
            initial["binary"] = self.defaults["binary"]

        loaded = server_config.load(initial["data_dir"])
        initial.update(
            {key: loaded[key] for key in ("listen", "public_url", "api_origin")}
        )
        self.defaults = server_config.defaults(Path(initial["binary"]))

        self.binary_dir = initial["binary"]
        self.data_dir = initial["data_dir"]
        self.backup_dir = initial["backup_dir"]
        self.listen = initial["listen"]
        self.public_url = initial["public_url"]
        self.api_origin = initial["api_origin"]

        self.users: list[parsing.User] = []
        self.selected: str | None = None
        self.rows: dict[str, widgets.UserRow] = {}
        self.server: cli.ServerProcess | None = None
        self.server_url: str | None = None
        self.server_address: str | None = None
        self.server_tail: list[str] = []
        self.server_stopping = False
        self.server_generation = 0
        self.auto_opened = False
        self.busy = False
        self.users_loading = False
        self.backups_loading = False
        self.backups: list[parsing.Backup] = []
        self.closing = False
        self.inbox: queue.Queue[Callable[[], None]] = queue.Queue()

        self._build()
        self.toast = widgets.Toast(self)

        self._drain_timer = QTimer(self)
        self._drain_timer.setInterval(30)
        self._drain_timer.timeout.connect(self._drain_inbox)
        self._drain_timer.start()

        self._update_buttons()
        QTimer.singleShot(0, self.bootstrap)

    # ---- 窗口骨架 -----------------------------------------------------

    def _make_icon(self) -> QIcon:
        pixmap = QPixmap(64, 64)
        pixmap.fill(Qt.GlobalColor.transparent)
        painter = QPainter(pixmap)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)
        painter.setPen(Qt.PenStyle.NoPen)
        painter.setBrush(QColor(theme.colors()["primary"]))
        painter.drawRoundedRect(QRectF(2, 2, 60, 60), 14, 14)
        renderer = QSvgRenderer(str(GUI_DIR / "assets" / "whale.svg"))
        renderer.render(painter, QRectF(12, 12, 40, 40))
        painter.end()
        return QIcon(pixmap)

    def _build(self) -> None:
        central = QWidget(self)
        self.setCentralWidget(central)
        outer = QVBoxLayout(central)
        outer.setContentsMargins(32, 22, 32, 16)
        outer.setSpacing(18)

        self._build_header(outer)
        self._build_service(outer)
        self._build_accounts(outer)
        self._build_data(outer)
        self._build_footer(outer)

    def _build_header(self, outer: QVBoxLayout) -> None:
        header = QWidget()
        row = QHBoxLayout(header)
        row.setContentsMargins(0, 0, 0, 4)
        row.setSpacing(14)

        logo = widgets.BrandLogo(GUI_DIR / "assets" / "whale.svg")
        row.addWidget(logo)

        titles = QVBoxLayout()
        titles.setContentsMargins(0, 0, 0, 0)
        titles.setSpacing(2)
        titles.addWidget(widgets.label("chat-deepseek", 22, 600))
        titles.addWidget(widgets.label("自托管服务管理", 12, 400, "muted"))
        row.addLayout(titles)
        row.addStretch(1)

        self.settings_button = widgets.button("设置", "secondary", height=34)
        self.settings_button.setMinimumWidth(84)
        self.settings_button.clicked.connect(self.open_settings)
        row.addWidget(self.settings_button, 0, Qt.AlignmentFlag.AlignVCenter)
        outer.addWidget(header)

    def _build_service(self, outer: QVBoxLayout) -> None:
        card = widgets.Card()
        self.service_card = card

        head = QHBoxLayout()
        head.setSpacing(10)
        head.addWidget(widgets.SectionHeading("服务"))
        self.status_detail = widgets.label("尚未启动", 11, 400, "muted")
        head.addWidget(self.status_detail)
        head.addStretch(1)
        self.status_pill = widgets.StatusPill()
        head.addWidget(self.status_pill, 0, Qt.AlignmentFlag.AlignVCenter)
        card.body.addLayout(head)

        actions = QHBoxLayout()
        actions.setSpacing(8)
        self.start_button = widgets.button("启动服务", "primary", height=36)
        self.start_button.setMinimumWidth(104)
        self.start_button.clicked.connect(self.start_server)
        actions.addWidget(self.start_button)
        self.stop_button = widgets.button("停止服务", "secondary", height=36)
        self.stop_button.setMinimumWidth(92)
        self.stop_button.clicked.connect(self.stop_server)
        actions.addWidget(self.stop_button)
        self.open_button = widgets.button("打开页面", "secondary", height=36)
        self.open_button.setMinimumWidth(92)
        self.open_button.clicked.connect(self.open_page)
        actions.addWidget(self.open_button)
        actions.addStretch(1)
        self.auto_switch = widgets.SwitchRow("启动后打开页面", True)
        actions.addWidget(self.auto_switch, 0, Qt.AlignmentFlag.AlignVCenter)
        card.body.addLayout(actions)

        strip = widgets.Panel(radius=20, background="card_alt", border=None)
        strip.setFixedHeight(40)
        strip_row = QHBoxLayout(strip)
        strip_row.setContentsMargins(12, 0, 12, 0)
        strip_row.setSpacing(10)
        strip_row.addWidget(widgets.label("数据目录", 11, 500, "muted"))
        self.data_summary = widgets.label("", 12)
        strip_row.addWidget(self.data_summary, 1)
        card.body.addWidget(strip)
        self._update_data_summary()

        outer.addWidget(card)

    def _build_accounts(self, outer: QVBoxLayout) -> None:
        card = widgets.Card()
        card.setSizePolicy(QSizePolicy.Policy.Expanding, QSizePolicy.Policy.Expanding)
        card.body.setContentsMargins(20, 14, 20, 14)
        self.accounts_card = card

        head = QHBoxLayout()
        head.setSpacing(8)
        head.addWidget(widgets.SectionHeading("账户"))
        count_chip = widgets.Panel(radius=11, background="card_alt", border=None)
        count_chip.setFixedHeight(22)
        count_chip.setMinimumWidth(30)
        chip_row = QHBoxLayout(count_chip)
        chip_row.setContentsMargins(8, 0, 8, 0)
        chip_row.setSpacing(0)
        self.user_count = widgets.label("", 11, 600, "muted")
        self.user_count.setAlignment(Qt.AlignmentFlag.AlignCenter)
        chip_row.addWidget(self.user_count, 1)
        head.addWidget(count_chip, 0, Qt.AlignmentFlag.AlignVCenter)
        head.addStretch(1)
        self.refresh_button = widgets.button(
            "刷新", "secondary", height=30, small=True
        )
        self.refresh_button.setMinimumWidth(62)
        self.refresh_button.clicked.connect(self.refresh_users)
        head.addWidget(self.refresh_button)
        self.add_button = widgets.button(
            "+ 添加", "primary", height=30, small=True
        )
        self.add_button.setMinimumWidth(74)
        self.add_button.clicked.connect(self.add_user)
        head.addWidget(self.add_button)
        card.body.addLayout(head)

        self.scroll = QScrollArea()
        self.scroll.setWidgetResizable(True)
        self.scroll.setFrameShape(QFrame.Shape.NoFrame)
        self.scroll.setHorizontalScrollBarPolicy(
            Qt.ScrollBarPolicy.ScrollBarAlwaysOff
        )
        self.scroll.setVerticalScrollBarPolicy(
            Qt.ScrollBarPolicy.ScrollBarAsNeeded
        )
        self.list_host = QWidget()
        self.list_layout = QVBoxLayout(self.list_host)
        self.list_layout.setContentsMargins(0, 2, 0, 0)
        self.list_layout.setSpacing(0)
        self.scroll.setWidget(self.list_host)
        card.body.addWidget(self.scroll, 1)

        dock = widgets.Panel(radius=18, background="card_alt", border=None)
        dock.setFixedHeight(58)
        bar = QHBoxLayout(dock)
        bar.setContentsMargins(12, 0, 8, 0)
        bar.setSpacing(8)
        info = QWidget(dock)
        info_column = QVBoxLayout(info)
        info_column.setContentsMargins(0, 0, 0, 0)
        info_column.setSpacing(1)
        self.selection_caption = widgets.label("账户操作", 10, 500, "muted")
        info_column.addWidget(self.selection_caption)
        self.selection_name = widgets.ElidedLabel(
            "选择一个账户", 13, 500, "muted", info
        )
        self.selection_name.setFixedWidth(110)
        info_column.addWidget(self.selection_name)
        bar.addWidget(info, 0, Qt.AlignmentFlag.AlignVCenter)
        self.selection_clear = widgets.button(
            "取消选择", "ghost", height=24, small=True
        )
        self.selection_clear.setAccessibleName("取消选择")
        self.selection_clear.clicked.connect(self.clear_selection)
        self.selection_clear.hide()
        bar.addWidget(self.selection_clear, 0, Qt.AlignmentFlag.AlignVCenter)
        bar.addStretch(1)
        self.action_buttons = [
            self._action_button(bar, "重置密码", self.reset_password),
            self._action_button(bar, "禁用", self.toggle_user),
            self._action_button(bar, "授予组", lambda: self.change_group(True)),
            self._action_button(bar, "撤销组", lambda: self.change_group(False)),
        ]
        bar.addSpacing(2)
        bar.addWidget(widgets.VerticalRule(18), 0, Qt.AlignmentFlag.AlignVCenter)
        bar.addSpacing(2)
        self.action_buttons.append(
            self._action_button(bar, "删除", self.delete_user, danger=True)
        )
        card.body.addWidget(dock)

        outer.addWidget(card, 1)

    def _build_data(self, outer: QVBoxLayout) -> None:
        card = widgets.Card()
        self.data_card = card
        bar = QHBoxLayout()
        bar.setSpacing(8)
        bar.addWidget(widgets.SectionHeading("数据"))
        bar.addStretch(1)
        self.backup_button = widgets.button(
            "立即备份", "primary", height=32, small=True
        )
        self.backup_button.setMinimumWidth(92)
        self.backup_button.clicked.connect(self.backup_default)
        bar.addWidget(self.backup_button)
        self.backup_records_button = widgets.button(
            "备份记录…", "secondary", height=32, small=True
        )
        self.backup_records_button.setMinimumWidth(96)
        self.backup_records_button.clicked.connect(self.manage_backups)
        bar.addWidget(self.backup_records_button)
        self.backup_as_button = widgets.button(
            "备份到…", "secondary", height=32, small=True
        )
        self.backup_as_button.setMinimumWidth(84)
        self.backup_as_button.clicked.connect(self.backup_as)
        bar.addWidget(self.backup_as_button)
        card.body.addLayout(bar)

        self.backup_label = widgets.ElidedLabel(
            "还没有备份记录", 12, 400, "muted"
        )
        card.body.addWidget(self.backup_label)
        outer.addWidget(card)

    def _build_footer(self, outer: QVBoxLayout) -> None:
        footer = QWidget()
        row = QHBoxLayout(footer)
        row.setContentsMargins(2, 0, 2, 0)
        row.setSpacing(8)
        self.version_label = widgets.label("chat-deepseek", 11, 400, "muted")
        row.addWidget(self.version_label)
        row.addStretch(1)
        self.help_button = widgets.button("命令行帮助", "ghost", height=28, small=True)
        self.help_button.setMinimumWidth(92)
        self.help_button.clicked.connect(self.show_help)
        row.addWidget(self.help_button)
        outer.addWidget(footer)

    # ---- 状态与提示 ---------------------------------------------------

    def _set_status(self, text: str, tone: str, detail: str | None = None) -> None:
        self.status_pill.set_state(text, tone)
        if detail is not None:
            self.status_detail.setText(detail)

    def _toast(self, message: str, error: bool = False) -> None:
        if not self.closing:
            self.toast.show_message(message, error)

    def _error(self, title: str, message: str, details: str | None = None) -> None:
        dialogs.show_message(self, title, message, details)

    def resizeEvent(self, event) -> None:  # noqa: N802
        super().resizeEvent(event)
        if hasattr(self, "toast"):
            self.toast.reposition()

    # ---- 异步执行 -----------------------------------------------------

    def _post(self, callback: Callable[[], None]) -> None:
        self.inbox.put(callback)

    def _drain_inbox(self) -> None:
        if self.closing:
            return
        while True:
            try:
                callback = self.inbox.get_nowait()
            except queue.Empty:
                break
            try:
                callback()
            except Exception as exc:  # noqa: BLE001 - 单个回调失败不应中断界面
                self._error("界面错误", str(exc))

    def _set_busy(self, busy: bool) -> None:
        if busy == self.busy:
            return
        self.busy = busy
        if busy:
            QApplication.setOverrideCursor(Qt.CursorShape.WaitCursor)
        else:
            QApplication.restoreOverrideCursor()
        self._update_buttons()

    def _execute(
        self,
        args: list[str],
        stdin_text: str | None = None,
        on_success: Callable[[cli.CommandResult], None] | None = None,
    ) -> None:
        if self.busy:
            self._toast("正在执行上一条命令", error=True)
            return
        try:
            binary = self._binary()
        except RuntimeError as exc:
            self._error("chat-deepseek", str(exc))
            return
        self._set_busy(True)

        def work() -> None:
            try:
                result = cli.run_command(binary, args, stdin_text)
            except Exception as exc:  # noqa: BLE001 - 直接呈现子进程错误
                result = cli.CommandResult(tuple(args), 1, "", str(exc))
            self._post(lambda: self._finish(result, on_success))

        threading.Thread(target=work, daemon=True).start()

    def _finish(
        self,
        result: cli.CommandResult,
        on_success: Callable[[cli.CommandResult], None] | None,
    ) -> None:
        if self.closing:
            return
        self._set_busy(False)
        if not result.ok:
            failure = parsing.explain_failure(
                result.stderr, result.stdout, result.args, result.code
            )
            self._error(failure.title, failure.message, failure.details)
            return
        if on_success is not None:
            try:
                on_success(result)
            except Exception as exc:  # noqa: BLE001 - 解析失败单独提示
                details = result.stdout.strip()
                self._error(
                    "未知错误",
                    "无法处理 chat-deepseek 返回的结果。",
                    f"{exc}\n\n{details}" if details else str(exc),
                )
        else:
            self._toast("完成")

    # ---- 参数 ---------------------------------------------------------

    def _binary(self) -> str:
        value = self.binary_dir.strip()
        if not value:
            raise RuntimeError("请选择 chat-deepseek 所在的二进制目录")
        path = Path(value).expanduser()
        if path.is_file():
            return str(path.resolve())
        candidate = cli.binary_in(path)
        if candidate.is_file():
            return str(candidate.resolve())
        raise RuntimeError(f"目录里没有 {cli.binary_name()}：{path}")

    def _data_args(self) -> list[str]:
        value = os.path.expanduser(self.data_dir.strip())
        return ["--data-dir", value] if value else []

    def _backup_args(self) -> list[str]:
        value = os.path.expanduser(self.backup_dir.strip())
        return ["--backup-dir", value] if value else []

    def _listen_args(self) -> list[str]:
        value = self.listen.strip()
        return ["--listen", value] if value else []

    def _data_root(self) -> Path:
        value = self.data_dir.strip()
        if value:
            return Path(value).expanduser()
        return Path(self._binary()).parent / "data"

    def _update_data_summary(self) -> None:
        default = Path(self.defaults["data_dir"]).expanduser()
        current = Path(self.data_dir).expanduser() if self.data_dir.strip() else default
        self.data_summary.setText("默认路径" if current == default else "自定义路径")

    def _page_url(self) -> str:
        if self.server_url:
            return self.server_url
        public = self.public_url.strip()
        if public:
            return public.rstrip("/")
        listen = self.listen.strip() or server_config.DEFAULT_LISTEN
        listen = listen.replace("[::]", "127.0.0.1").replace("0.0.0.0", "127.0.0.1")
        if listen.startswith(":"):
            listen = "127.0.0.1" + listen
        if listen.isdigit():
            listen = "127.0.0.1:" + listen
        return "http://" + listen

    # ---- 启动 ---------------------------------------------------------

    def bootstrap(self) -> None:
        try:
            self._binary()
        except RuntimeError as exc:
            self._error(
                "找不到 chat-deepseek",
                "请在设置里选择 chat-deepseek 所在的二进制目录。",
                str(exc),
            )
            return
        self.refresh_users()
        self._load_version()
        self.refresh_backups()

    def _load_version(self) -> None:
        try:
            binary = self._binary()
        except RuntimeError:
            return

        def work() -> None:
            try:
                result = cli.run_command(binary, ["version"])
            except Exception:  # noqa: BLE001 - 版本只影响页脚
                return
            self._post(lambda: self._apply_version(result))

        threading.Thread(target=work, daemon=True).start()

    def _apply_version(self, result: cli.CommandResult) -> None:
        if self.closing:
            return
        version = parsing.parse_version(result.stdout)
        if version:
            self.version_label.setText(f"chat-deepseek {version[0]} · {version[1]}")
        else:
            self.version_label.setText("chat-deepseek · 版本未知")

    # ---- 账户 ---------------------------------------------------------

    def refresh_users(self) -> None:
        if self.users_loading or self.busy:
            return
        try:
            binary = self._binary()
        except RuntimeError:
            return
        self.users_loading = True
        self.user_count.setText("…")
        self._update_buttons()
        args = ["user", "list", *self._data_args()]

        def work() -> None:
            try:
                result = cli.run_command(binary, args)
            except Exception as exc:  # noqa: BLE001
                result = cli.CommandResult(tuple(args), 1, "", str(exc))
            self._post(lambda: self._apply_users(result))

        threading.Thread(target=work, daemon=True).start()

    def _apply_users(self, result: cli.CommandResult) -> None:
        if self.closing:
            return
        self.users_loading = False
        self._update_buttons()
        if not result.ok:
            self.user_count.setText("")
            failure = parsing.explain_failure(
                result.stderr, result.stdout, result.args, result.code
            )
            self._error(failure.title, failure.message, failure.details)
            return
        try:
            users = parsing.parse_users(result.stdout)
        except parsing.ParseError as exc:
            self.user_count.setText("")
            failure = parsing.unknown_output(
                "无法识别 chat-deepseek 返回的账户列表。",
                result.stdout,
                result.stderr,
            )
            details = failure.details or ""
            self._error(
                failure.title,
                failure.message,
                f"{exc}\n\n{details}" if details else str(exc),
            )
            return
        self.users = users
        self.user_count.setText(str(len(users)))
        self._render_users()

    def _clear_list(self) -> None:
        while self.list_layout.count():
            item = self.list_layout.takeAt(0)
            widget = item.widget()
            if widget is not None:
                widget.deleteLater()

    def _render_users(self) -> None:
        self._clear_list()
        self.rows.clear()
        self.scroll.setVerticalScrollBarPolicy(
            Qt.ScrollBarPolicy.ScrollBarAsNeeded
            if self.users
            else Qt.ScrollBarPolicy.ScrollBarAlwaysOff
        )
        if self.selected and not any(user.username == self.selected for user in self.users):
            self.selected = None

        if not self.users:
            empty = QWidget()
            column = QVBoxLayout(empty)
            column.setContentsMargins(14, 8, 14, 8)
            column.setSpacing(6)
            column.addStretch(1)
            column.addWidget(
                widgets.Avatar("+", size=40), 0, Qt.AlignmentFlag.AlignHCenter
            )
            title = widgets.label("还没有账户", 15, 600)
            title.setAlignment(Qt.AlignmentFlag.AlignCenter)
            column.addWidget(title)
            hint = widgets.label("创建第一个 Owner，就能在浏览器里登录。", 12, 400, "muted")
            hint.setAlignment(Qt.AlignmentFlag.AlignCenter)
            column.addWidget(hint)
            self.setup_button = widgets.button(
                "创建 Owner", "primary", height=32, small=True
            )
            self.setup_button.setMinimumWidth(104)
            self.setup_button.clicked.connect(self.setup_owner)
            column.addWidget(
                self.setup_button, 0, Qt.AlignmentFlag.AlignHCenter
            )
            column.addStretch(1)
            self.list_layout.addStretch(1)
            self.list_layout.addWidget(empty)
            self.list_layout.addStretch(1)
        else:
            self.setup_button = None
            for user in self.users:
                row = widgets.UserRow(user)
                row.clicked.connect(self.select_user)
                self.rows[user.username] = row
                self.list_layout.addWidget(row)
            self.list_layout.addStretch(1)
        self._paint_selection()

    def _paint_selection(self) -> None:
        for username, row in self.rows.items():
            row.set_selected(username == self.selected)
        user = self._selected_user()
        active = user is not None
        self.selection_caption.setText("已选择" if active else "账户操作")
        self.selection_caption.setFont(
            theme.font(10, 600 if active else 500)
        )
        self.selection_caption.setProperty(
            "tone", "primary" if active else "muted"
        )
        self.selection_name.setFont(
            theme.font(13, 600 if active else 500)
        )
        self.selection_name.setProperty(
            "tone",
            "muted" if user is None or user.disabled else "text",
        )
        self.selection_name.setText(user.username if user else "选择一个账户")
        self.selection_clear.setVisible(active)
        theme.repolish(self.selection_caption)
        theme.repolish(self.selection_name)
        self._update_buttons()

    def select_user(self, username: str) -> None:
        if username not in self.rows:
            return
        self.selected = None if self.selected == username else username
        self._paint_selection()

    def clear_selection(self) -> None:
        if self.selected is None:
            return
        self.selected = None
        self._paint_selection()

    def _action_button(
        self,
        layout: QHBoxLayout,
        text: str,
        command: Callable[[], None],
        danger: bool = False,
    ):
        button = widgets.button(
            text, "danger" if danger else "secondary", height=32, small=True
        )
        button.setMinimumWidth(74)
        button.clicked.connect(command)
        layout.addWidget(button)
        return button

    def _update_buttons(self) -> None:
        if not hasattr(self, "start_button"):
            return
        running = self.server is not None and self.server.running
        busy = self.busy
        self.start_button.setEnabled(not running and not busy)
        self.stop_button.setEnabled(
            running and not self.server_stopping and not busy
        )
        self.open_button.setEnabled((running or self.server_url is not None) and not busy)
        self.refresh_button.setEnabled(not busy and not self.users_loading)
        self.add_button.setEnabled(not busy and bool(self.users))
        self.settings_button.setEnabled(not busy)
        self.backup_button.setEnabled(not busy)
        self.backup_records_button.setEnabled(not busy)
        self.backup_as_button.setEnabled(not busy)
        self.help_button.setEnabled(not busy)
        if getattr(self, "setup_button", None) is not None:
            self.setup_button.setEnabled(not busy)

        selected = self._selected_user()
        for button in self.action_buttons:
            button.setEnabled(selected is not None and not busy)
        if len(self.action_buttons) > 1:
            self.action_buttons[1].setText(
                "启用" if selected is not None and selected.disabled else "禁用"
            )

    def _selected_user(self) -> parsing.User | None:
        return next(
            (user for user in self.users if user.username == self.selected), None
        )

    def setup_owner(self) -> None:
        dialog = dialogs.AccountDialog(self, "owner")
        dialog.exec_modal()
        if not dialog.value:
            return
        name, password, _groups = dialog.value
        self._execute(
            [
                "setup",
                "--username",
                name,
                "--password-stdin",
                *self._data_args(),
                *self._listen_args(),
            ],
            password + "\n",
            on_success=lambda _result: self.refresh_users(),
        )

    def add_user(self) -> None:
        dialog = dialogs.AccountDialog(self, "user")
        dialog.exec_modal()
        if not dialog.value:
            return
        name, password, groups = dialog.value
        args = ["user", "add", *self._data_args()]
        for group in groups:
            args += ["--group", group]
        args += ["--password-stdin", name]
        self._execute(
            args,
            password + "\n",
            on_success=lambda _result: self.refresh_users(),
        )

    def reset_password(self) -> None:
        if self.selected is None:
            return
        dialog = dialogs.PasswordDialog(self, "重置密码", self.selected)
        dialog.exec_modal()
        if dialog.value is None:
            return
        self._execute(
            [
                "user",
                "reset-password",
                *self._data_args(),
                "--password-stdin",
                self.selected,
            ],
            str(dialog.value) + "\n",
            on_success=lambda _result: self._toast("密码已重置，旧会话已吊销"),
        )

    def toggle_user(self) -> None:
        user = self._selected_user()
        if user is None:
            return
        action = "enable" if user.disabled else "disable"
        self._execute(
            ["user", action, *self._data_args(), user.username],
            on_success=lambda _result: self._after_user_changed(
                "已启用" if user.disabled else "已禁用"
            ),
        )

    def change_group(self, add: bool) -> None:
        user = self._selected_user()
        if user is None:
            return
        dialog = dialogs.GroupDialog(self, user.username, add)
        dialog.exec_modal()
        if dialog.value is None:
            return
        group = str(dialog.value)
        action = "grant" if add else "revoke"
        self._execute(
            ["user", action, *self._data_args(), user.username, group],
            on_success=lambda _result: self._after_user_changed(
                f"已{'授予' if add else '撤销'} {group}"
            ),
        )

    def delete_user(self) -> None:
        user = self._selected_user()
        if user is None:
            return
        if not dialogs.ask_confirm(
            self,
            "删除账户",
            f"永久删除账户 {user.username}？\n\n其会话会删除；附件文件不会自动删除，仍留在数据目录。",
            danger=True,
        ):
            return
        self._execute(
            ["user", "delete", *self._data_args(), "--yes", user.username],
            on_success=lambda _result: self._after_user_changed("账户已删除"),
        )

    def _after_user_changed(self, message: str) -> None:
        self._toast(message)
        self.refresh_users()

    # ---- 服务 ---------------------------------------------------------

    def start_server(self) -> None:
        if self.server is not None and self.server.running:
            return
        try:
            binary = self._binary()
        except RuntimeError as exc:
            self._error("chat-deepseek", str(exc))
            return
        # 服务端配置由 serve 从 data/config.json 读取；GUI 只负责把设置写进去。
        args = ["serve", "--no-browser", *self._data_args()]

        self.server_url = None
        self.server_address = None
        self.server_tail = []
        self.server_stopping = False
        self.auto_opened = False
        self.server_generation += 1
        generation = self.server_generation
        server = cli.ServerProcess(
            binary,
            args,
            on_line=lambda line: self._post(
                lambda: self._handle_serve_line(generation, line)
            ),
            on_exit=lambda code: self._post(
                lambda: self._handle_serve_exit(generation, code)
            ),
        )
        self.server = server
        try:
            server.start()
        except Exception as exc:  # noqa: BLE001 - 启动失败直接提示
            self.server = None
            failure = parsing.explain_failure(str(exc), "", ("serve",))
            self._error(failure.title, failure.message, failure.details)
            return
        self._set_status("启动中…", "warning", "等待监听地址")
        self._update_buttons()

    def _handle_serve_line(self, generation: int, line: str) -> None:
        if generation != self.server_generation or self.server is None:
            return
        self.server_tail = (self.server_tail + [line])[-40:]
        event = parsing.parse_serve_event(line)
        if event.kind == "listening":
            self.server_address = event.value
            self._set_status("运行中", "success", f"监听 {event.value}")
        elif event.kind == "url":
            self.server_url = event.value
            self._set_status("运行中", "success", event.value)
            if self.auto_switch.isChecked() and not self.auto_opened:
                self.auto_opened = True
                self._open_url(event.value)
        elif event.kind == "stopping":
            self._set_status("正在停止…", "warning", "正在关闭服务")
        self._update_buttons()

    def _handle_serve_exit(self, generation: int, code: int) -> None:
        if generation != self.server_generation:
            return
        was_stopping = self.server_stopping
        self.server = None
        self.server_url = None
        self.server_address = None
        self.server_stopping = False
        self.auto_opened = False
        self._set_status("未运行", "muted", "尚未启动")
        self._update_buttons()
        if code != 0 and not was_stopping and not self.closing:
            failure = parsing.explain_serve_failure(self.server_tail, code)
            self._error(failure.title, failure.message, failure.details)
        elif was_stopping:
            self._toast("服务已停止")

    def stop_server(self) -> None:
        if self.server is None or not self.server.running:
            self._toast("服务没有在运行", error=True)
            return
        self.server_stopping = True
        self._set_status("正在停止…", "warning", "正在关闭服务")
        self._update_buttons()
        self.server.stop()

    def open_page(self) -> None:
        if self.server is None and self.server_url is None:
            self._toast("服务没有在运行", error=True)
            return
        self._open_url(self._page_url())

    def _open_url(self, url: str) -> None:
        QDesktopServices.openUrl(QUrl(url))
        self._toast(f"已打开 {url}")

    # ---- 备份与帮助 ---------------------------------------------------

    def backup_default(self) -> None:
        self._execute(
            ["backup", *self._data_args(), *self._backup_args()],
            on_success=self._after_backup,
        )

    def backup_as(self) -> None:
        try:
            root = self._data_root()
        except RuntimeError:
            root = Path.home()
        initial = Path(
            os.path.expanduser(self.backup_dir.strip() or str(root / "backups"))
        )
        if not initial.is_dir():
            initial = root if root.is_dir() else Path.home()
        name = f"chat-deepseek-{time.strftime('%Y%m%d-%H%M%S')}.zip"
        target = dialogs.choose_save_path(self, "保存备份", str(initial), name)
        if target is None:
            return
        self._execute(
            [
                "backup",
                "--output",
                target,
                *self._data_args(),
                *self._backup_args(),
            ],
            on_success=self._after_backup,
        )

    def refresh_backups(self) -> None:
        if self.backups_loading or self.closing:
            return
        try:
            binary = self._binary()
        except RuntimeError:
            return
        self.backups_loading = True
        args = ["backup", "list", *self._data_args(), *self._backup_args()]

        def work() -> None:
            try:
                result = cli.run_command(binary, args)
            except Exception as exc:  # noqa: BLE001 - 直接呈现在信息栏
                result = cli.CommandResult(tuple(args), 1, "", str(exc))
            self._post(lambda: self._apply_backup_summary(result))

        threading.Thread(target=work, daemon=True).start()

    def _apply_backup_summary(self, result: cli.CommandResult) -> None:
        if self.closing:
            return
        self.backups_loading = False
        if not result.ok:
            failure = parsing.explain_failure(
                result.stderr, result.stdout, result.args, result.code
            )
            self.backup_label.setText("无法读取备份记录")
            self.backup_label.setToolTip(failure.message)
            return
        try:
            archives = parsing.parse_backup_list(result.stdout)
        except parsing.ParseError:
            self.backup_label.setText("无法读取备份记录")
            self.backup_label.setToolTip("备份列表格式无法识别")
            return
        self.backups = archives
        if archives:
            latest = archives[0]
            self.backup_label.setText(
                f"最近备份：{latest.time:%Y-%m-%d %H:%M:%S}"
                f"（{latest.size}）"
            )
        else:
            self.backup_label.setText("还没有备份记录")

    def manage_backups(self) -> None:
        self._execute(
            ["backup", "list", *self._data_args(), *self._backup_args()],
            on_success=self._open_backup_records,
        )

    def _open_backup_records(self, result: cli.CommandResult) -> None:
        try:
            archives = parsing.parse_backup_list(result.stdout)
        except parsing.ParseError as exc:
            failure = parsing.unknown_output(
                "无法识别 chat-deepseek 返回的备份列表。",
                result.stdout,
                result.stderr,
            )
            details = failure.details or ""
            self._error(
                failure.title,
                failure.message,
                f"{exc}\n\n{details}" if details else str(exc),
            )
            return
        self.backups = archives
        if archives:
            latest = archives[0]
            self.backup_label.setText(
                f"最近备份：{latest.time:%Y-%m-%d %H:%M:%S}"
                f"（{latest.size}）"
            )
        dialog = dialogs.BackupDialog(self, self.backup_dir, archives)
        dialog.exec_modal()
        if not isinstance(dialog.value, str):
            return
        name = dialog.value
        if not dialogs.ask_confirm(
            self,
            "恢复备份",
            f"用备份 {name} 覆盖当前数据？\n\n"
            "会覆盖 chat.db、config.json、master.key 和 files/；"
            "请确认服务已经停止。",
            danger=True,
        ):
            return
        self._execute(
            [
                "backup",
                "restore",
                "--yes",
                *self._data_args(),
                *self._backup_args(),
                name,
            ],
            on_success=lambda _result: self._after_restore(name),
        )

    def _after_restore(self, name: str) -> None:
        self.selected = None
        stamp = parsing.parse_backup_time(name)
        label = stamp.strftime("%Y-%m-%d %H:%M:%S") if stamp else name
        self.backup_label.setText(f"最近恢复：{label}")
        self._toast("备份已恢复，请重新启动服务")
        self.refresh_users()

    def _after_backup(self, result: cli.CommandResult) -> None:
        path = parsing.parse_backup_path(result.stdout)
        if path is None:
            failure = parsing.unknown_output(
                "备份命令已经执行，但没有解析到备份路径。",
                result.stdout,
                result.stderr,
            )
            self._error("无法识别备份结果", failure.message, failure.details)
            return
        target = Path(path)
        stamp = parsing.parse_backup_time(path)
        label = stamp.strftime("%Y-%m-%d %H:%M:%S") if stamp else str(target)
        if target.is_file():
            human = parsing.format_size(target.stat().st_size)
            text = f"最近备份：{label}（{human}）"
        else:
            text = f"最近备份：{label}"
        self.backup_label.setText(text)
        self._toast("备份完成")

    def show_help(self) -> None:
        self._execute(
            ["help"],
            on_success=lambda result: self._error(
                "命令行提示", "完整参数如下：", result.stdout
            ),
        )

    # ---- 设置 ---------------------------------------------------------

    def open_settings(self) -> None:
        dialog = dialogs.SettingsDialog(
            self,
            self.binary_dir,
            self.data_dir,
            self.backup_dir,
            self.listen,
            self.public_url,
            self.api_origin,
            self.defaults,
        )
        dialog.exec_modal()
        if not isinstance(dialog.value, dict):
            return
        value = dialog.value
        binary = str(value["binary"])
        data_dir = str(value["data_dir"])
        backup_dir = str(value["backup_dir"])
        listen = str(value["listen"])
        public_url = str(value["public_url"])
        api_origin = str(value["api_origin"])
        try:
            server_config.save(
                data_dir,
                {
                    "listen": listen,
                    "public_url": public_url,
                    "api_origin": api_origin,
                },
            )
            gui_config.save(
                self.gui_config_path,
                {
                    "binary": binary,
                    "data_dir": data_dir,
                    "backup_dir": backup_dir,
                },
            )
        except OSError as exc:
            self._error("保存设置失败", f"无法写入配置文件：{exc}")
            return
        self.binary_dir = binary
        self.data_dir = data_dir
        self.backup_dir = backup_dir
        self.listen = listen
        self.public_url = public_url
        self.api_origin = api_origin
        self.defaults = server_config.defaults(Path(self.binary_dir))
        self._update_data_summary()
        if self.server is not None and self.server.running:
            self._toast("设置已保存；服务重启后生效")
        else:
            self._toast("设置已保存")
        self._load_version()
        self.refresh_users()
        self.refresh_backups()

    # ---- 关闭 ---------------------------------------------------------

    def closeEvent(self, event) -> None:  # noqa: N802
        if self.server is not None and self.server.running:
            if not dialogs.ask_confirm(
                self, "退出", "服务还在运行。停止服务并退出？", danger=True
            ):
                event.ignore()
                return
            self.server_stopping = True
            self.server.stop(wait=True, timeout=5)
        self.closing = True
        self._drain_timer.stop()
        event.accept()


def main() -> None:
    QApplication.setHighDpiScaleFactorRoundingPolicy(
        Qt.HighDpiScaleFactorRoundingPolicy.PassThrough
    )
    application = QApplication(sys.argv)
    application.setApplicationName("chat-deepseek")
    theme.install(application)
    window = MainWindow()
    window.show()
    raise SystemExit(application.exec())


if __name__ == "__main__":
    main()
