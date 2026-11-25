#!/usr/bin/env python3
"""chat-deepseek 的管理界面（PySide6）。

界面只负责调用 chat-deepseek 子进程并展示结果；参数拼装、路径和密码处理都在
CLI 侧完成。所有阻塞操作放在后台线程，主线程只处理界面状态。
"""

from __future__ import annotations

import os
import queue
import shlex
import sys
import threading
import time
from datetime import datetime
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
import service_plan
import ssh_session
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

        saved = gui_config.load(self.gui_config_path)
        if saved.empty:
            for seed in gui_config.seed_paths():
                candidate = gui_config.load(seed)
                if not candidate.empty:
                    saved = candidate
                    break
        self.connection = (
            saved.connection
            if saved.connection in gui_config.CONNECTIONS
            else gui_config.DEFAULT_CONNECTION
        )
        self.legacy_backup_dir = saved.legacy_backup_dir
        self.config_migration_error: str | None = None
        if saved.migrated:
            try:
                gui_config.save(self.gui_config_path, saved)
            except OSError as exc:
                self.config_migration_error = str(exc)

        # 本机和 SSH 各存一份路径，切换连接时不用重新输入。
        self.local_profile = {
            "binary_dir": saved.local.binary_dir,
            "data_dir": saved.local.data_dir,
            "username": saved.local.username,
        }
        self.ssh_profile = {
            "host": saved.ssh.host,
            "binary_dir": saved.ssh.binary_dir,
            "data_dir": saved.ssh.data_dir,
            "username": saved.ssh.username,
        }
        # 远端主机被清空时退回本机，避免一启动就报“没有配置 SSH 主机”。
        if self.connection == "ssh" and not self.ssh_profile["host"]:
            self.connection = gui_config.DEFAULT_CONNECTION

        local_binary = self.local_profile["binary_dir"] or self.defaults["binary"]
        if not Path(local_binary).expanduser().is_file() and not cli.binary_in(
            local_binary
        ).is_file():
            local_binary = self.defaults["binary"]
        local_data = self.local_profile["data_dir"] or str(
            server_config.default_data_dir(local_binary)
        )
        self.local_profile["binary_dir"] = local_binary
        self.local_profile["data_dir"] = local_data
        # 服务端配置统一由 `chat-deepseek info` 读取；这里先填默认值。
        backup_default = server_config.default_backup_dir(local_data)
        self.defaults = server_config.defaults(Path(local_binary))

        self.binary_dir = (
            self.ssh_profile["binary_dir"]
            if self.remote
            else self.local_profile["binary_dir"]
        )
        self.data_dir = (
            self.ssh_profile["data_dir"]
            if self.remote
            else self.local_profile["data_dir"]
        )
        self.last_username = (
            self.ssh_profile["username"]
            if self.remote
            else self.local_profile["username"]
        )
        # 备份地址属于目标主机自己的 config.json（由 info 读回）。远端在 info
        # 返回前先留空，不能拿 GUI 所在机器的默认路径顶上。
        self.backup_dir = "" if self.remote else backup_default
        self.listen = self.defaults["listen"]
        self.public_url = self.defaults["public_url"]
        self.api_origin = self.defaults["api_origin"]
        self.info: parsing.Info | None = None
        self.info_generation = 0
        self.service_supported = False
        self.service_installed = False
        self.service_active = False
        self.service_enabled = False
        self.service_linger = False

        self.users: list[parsing.User] = []
        self.selected: str | None = None
        self.rows: dict[str, widgets.UserRow] = {}
        self.identity: parsing.Identity | None = None
        self.data_ready = False
        self.session_generation = 0
        self.server: cli.ServerProcess | None = None
        self.ssh: ssh_session.SshSession | None = None
        self._ssh_lock = threading.Lock()
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
        self.setup_button = None
        self.settings_pending = False
        self.pending_action: Callable[[], None] | None = None
        self.inbox: queue.Queue[Callable[[], None]] = queue.Queue()

        self._build()
        self.toast = widgets.Toast(self)

        self._drain_timer = QTimer(self)
        self._drain_timer.setInterval(30)
        self._drain_timer.timeout.connect(self._drain_inbox)
        self._drain_timer.start()

        self._identity_timer = QTimer(self)
        self._identity_timer.setInterval(1000)
        self._identity_timer.timeout.connect(self._tick_identity)
        self._identity_timer.start()

        self._update_identity_ui()
        self._update_buttons()
        QTimer.singleShot(0, self.bootstrap)

    # ---- 窗口骨架 -----------------------------------------------------

    @property
    def remote(self) -> bool:
        """当前是否管理远端主机。"""
        return self.connection == "ssh"

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

        self.identity_lock = widgets.LockGlyph(18)
        row.addWidget(self.identity_lock, 0, Qt.AlignmentFlag.AlignVCenter)
        self.identity_label = widgets.ElidedLabel("未登录", 12, 500, "muted")
        self.identity_label.setMinimumWidth(260)
        self.identity_label.setMaximumWidth(320)
        self.identity_label.setAccessibleName("登录状态")
        row.addWidget(self.identity_label, 0, Qt.AlignmentFlag.AlignVCenter)
        self.identity_button = widgets.button(
            "登录", "secondary", height=30, small=True
        )
        self.identity_button.setMinimumWidth(64)
        self.identity_button.clicked.connect(self.toggle_identity)
        row.addWidget(self.identity_button, 0, Qt.AlignmentFlag.AlignVCenter)
        row.addSpacing(6)

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
        head.addSpacing(4)
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
        self.restart_button = widgets.button("重启服务", "secondary", height=36)
        self.restart_button.setMinimumWidth(92)
        self.restart_button.setToolTip("重启 systemd 用户服务")
        self.restart_button.clicked.connect(self.restart_server)
        self.restart_button.hide()
        actions.addWidget(self.restart_button)
        self.install_button = widgets.button("安装服务", "secondary", height=36)
        self.install_button.setMinimumWidth(92)
        self.install_button.setToolTip("安装 systemd 服务")
        self.install_button.clicked.connect(self.install_service)
        self.install_button.hide()
        actions.addWidget(self.install_button)
        self.update_button = widgets.button("更新服务", "secondary", height=36)
        self.update_button.setMinimumWidth(92)
        self.update_button.setToolTip("按当前二进制和数据目录重写 systemd unit")
        self.update_button.clicked.connect(self.update_service)
        self.update_button.hide()
        actions.addWidget(self.update_button)
        self.uninstall_button = widgets.button("卸载服务", "secondary", height=36)
        self.uninstall_button.setMinimumWidth(92)
        self.uninstall_button.setToolTip("停止并删除 systemd unit，不删除数据")
        self.uninstall_button.clicked.connect(self.uninstall_service)
        self.uninstall_button.hide()
        actions.addWidget(self.uninstall_button)
        self.open_button = widgets.button("打开页面", "secondary", height=36)
        self.open_button.setMinimumWidth(92)
        self.open_button.clicked.connect(self.open_page)
        actions.addWidget(self.open_button)
        actions.addStretch(1)
        self.auto_switch = widgets.SwitchRow("启动后打开页面", True)
        actions.addWidget(self.auto_switch, 0, Qt.AlignmentFlag.AlignVCenter)
        card.body.addLayout(actions)

        self.info_strip = widgets.Panel(radius=20, background="card_alt", border=None)
        self.info_strip.setFixedHeight(40)
        info_column = QVBoxLayout(self.info_strip)
        info_column.setContentsMargins(12, 6, 12, 6)
        info_column.setSpacing(2)

        data_row = QHBoxLayout()
        data_row.setSpacing(10)
        data_row.addWidget(widgets.label("数据目录", 11, 500, "muted"))
        self.data_summary = widgets.label("", 12)
        data_row.addWidget(self.data_summary, 1)
        info_column.addLayout(data_row)

        self.unit_row = QWidget()
        unit_row = QHBoxLayout(self.unit_row)
        unit_row.setContentsMargins(0, 0, 0, 0)
        unit_row.setSpacing(10)
        unit_row.addWidget(widgets.label("安装位置", 11, 500, "muted"))
        self.unit_summary = widgets.ElidedLabel("", 12)
        unit_row.addWidget(self.unit_summary, 1)
        self.enabled_badge = widgets.StatusBadge("", "muted")
        self.enabled_badge.setAttribute(
            Qt.WidgetAttribute.WA_TransparentForMouseEvents, False
        )
        unit_row.addWidget(self.enabled_badge, 0, Qt.AlignmentFlag.AlignVCenter)
        self.linger_badge = widgets.StatusBadge("", "muted")
        self.linger_badge.setAttribute(
            Qt.WidgetAttribute.WA_TransparentForMouseEvents, False
        )
        unit_row.addWidget(self.linger_badge, 0, Qt.AlignmentFlag.AlignVCenter)
        self.unit_row.hide()
        info_column.addWidget(self.unit_row)
        card.body.addWidget(self.info_strip)
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
        self.count_chip = count_chip
        head.addStretch(1)
        self.refresh_button = widgets.button(
            "刷新", "secondary", height=30, small=True
        )
        self.refresh_button.setMinimumWidth(62)
        self.refresh_button.clicked.connect(self.refresh_accounts)
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
            "登录后查看备份记录", 12, 400, "muted"
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

    # ---- 登录态 -------------------------------------------------------

    def _data_ready(self) -> bool:
        if self.remote:
            # 远端数据目录在服务器上，本地无法 stat；以 `info` 的 initialized 为准。
            return bool(self.info and self.info.initialized)
        try:
            return (self._data_root() / "chat.db").is_file()
        except RuntimeError:
            return False

    def _refresh_data_state(self) -> None:
        self.data_ready = self._data_ready()

    def _save_gui_config(self) -> None:
        gui_config.save(
            self.gui_config_path,
            gui_config.Config(
                connection=self.connection,
                local=gui_config.LocalConfig(**self.local_profile),
                ssh=gui_config.SshConfig(**self.ssh_profile),
            ),
        )

    def _update_identity_ui(self) -> None:
        if self.identity is None:
            self.identity_lock.show()
            self.identity_label.setText("未登录")
            self.identity_label.setToolTip("登录后才能管理账户和备份")
            self.identity_button.setText("登录")
            self.identity_button.setToolTip("登录 CLI 管理账户")
            return
        self.identity_lock.hide()
        self.identity_label.setText(
            f"{self.identity.username}（{self.identity.role}）"
            f" · {self._remaining_text()}"
        )
        self.identity_label.setToolTip(
            f"账户：{self.identity.username}\n"
            f"组：{self.identity.groups_text}\n"
            f"过期：{self.identity.expires_at:%Y-%m-%d %H:%M:%S}"
        )
        self.identity_button.setText("退出")
        self.identity_button.setToolTip("退出当前登录")

    def _remaining_text(self) -> str:
        if self.identity is None:
            return ""
        seconds = int(
            (self.identity.expires_at - datetime.now()).total_seconds()
        )
        if seconds <= 0:
            return "已过期"
        if seconds < 60:
            return f"{seconds} 秒后过期"
        return f"{seconds // 60} 分钟后过期"

    def _tick_identity(self) -> None:
        if self.closing or self.identity is None:
            return
        if self.identity.expires_at <= datetime.now():
            self._reset_identity_state("登录已过期，请重新登录")
            return
        self._update_identity_ui()

    def _reset_identity_state(self, message: str | None = None) -> None:
        """退回未登录状态；递增代数让在途的列表请求作废。"""
        self.session_generation += 1
        self.identity = None
        self.users = []
        self.selected = None
        self.backups = []
        self.pending_action = None
        self.users_loading = False
        self.backups_loading = False
        self._render_account_state()
        self._update_identity_ui()
        self._update_backup_placeholder()
        self._update_buttons()
        if message and not self.closing:
            self._toast(message, error=True)

    def _ensure_login(self, then: Callable[[], None] | None = None) -> bool:
        """已登录返回 True；否则弹出登录框，并在成功后执行 then。"""
        if self.identity is not None:
            return True
        if not self.data_ready:
            self._error(
                "数据目录还没有初始化",
                "请先创建 Owner，或从备份恢复后再登录。",
            )
            return False
        self._login(then=then)
        return False

    def _require_owner(self) -> bool:
        if self.identity is not None and self.identity.is_owner:
            return True
        self._toast("需要 Owner 权限", error=True)
        return False

    def _login(
        self,
        then: Callable[[], None] | None = None,
        username: str | None = None,
        error: str | None = None,
    ) -> None:
        if self.busy:
            self._toast("正在执行上一条命令", error=True)
            return
        if not self.data_ready:
            self._error(
                "数据目录还没有初始化",
                "请先创建 Owner，或从备份恢复后再登录。",
            )
            return
        dialog = dialogs.LoginDialog(
            self,
            username if username is not None else self.last_username,
            error,
        )
        dialog.exec_modal()
        if not dialog.value:
            return
        name, password = dialog.value
        self._execute(
            [
                "login",
                "--username",
                name,
                "--password-stdin",
                *self._data_args(),
            ],
            password + "\n",
            on_success=lambda _result: self._after_login(name, then),
            on_failure=lambda failure, _result: self._login(
                then, name, failure.message
            ),
        )

    def _after_login(
        self,
        username: str,
        then: Callable[[], None] | None,
    ) -> None:
        self.last_username = username
        if self.remote:
            self.ssh_profile["username"] = username
        else:
            self.local_profile["username"] = username
        try:
            self._save_gui_config()
        except OSError:
            pass
        self._refresh_identity(then)

    def _refresh_identity(
        self, then: Callable[[], None] | None = None
    ) -> None:
        try:
            self._binary()
        except RuntimeError:
            return
        generation = self.session_generation
        args = ["whoami", *self._data_args()]

        def work() -> None:
            try:
                result = self._run(args)
            except Exception as exc:  # noqa: BLE001
                result = cli.CommandResult(tuple(args), 1, "", str(exc))
            self._post(lambda: self._apply_identity(result, generation, then))

        threading.Thread(target=work).start()

    def _apply_identity(
        self,
        result: cli.CommandResult,
        generation: int,
        then: Callable[[], None] | None,
    ) -> None:
        if self.closing or generation != self.session_generation:
            return
        if not result.ok:
            if self._ssh_failure(result):
                return
            if result.code == 3:
                self._reset_identity_state("登录已失效，请重新登录")
            else:
                failure = parsing.explain_failure(
                    result.stderr, result.stdout, result.args, result.code
                )
                self._error(failure.title, failure.message, failure.details)
            return
        try:
            identity = parsing.parse_whoami(result.stdout)
        except parsing.ParseError as exc:
            failure = parsing.unknown_output(
                "无法识别 chat-deepseek 返回的登录身份。",
                result.stdout,
                result.stderr,
            )
            self._reset_identity_state()
            self._error(
                failure.title,
                failure.message,
                f"{exc}\n\n{failure.details or ''}".strip(),
            )
            return
        self.session_generation += 1
        self.identity = identity
        self.last_username = identity.username
        self.data_ready = True
        self.users_loading = False
        self.backups_loading = False
        if not identity.is_admin:
            self._reset_identity_state()
            self._error(
                "没有管理权限",
                "当前账户已不是 Admin 或 Owner，请重新登录有权限的账户。",
            )
            return
        try:
            self._save_gui_config()
        except OSError:
            pass
        self._update_identity_ui()
        self._update_buttons()
        if then is not None:
            self.pending_action = then
        self.refresh_users()
        self.refresh_backups()
        self._run_pending_action()

    def _run_pending_action(self) -> None:
        """登录后先让账户/备份列表加载完，再执行用户点的那一步。

        否则 ``backup`` 会和 ``backup list`` 并发打开 SQLite，出现假的
        “服务正在运行”错误。
        """
        if (
            self.pending_action is None
            or self.users_loading
            or self.backups_loading
            or self.busy
        ):
            return
        action = self.pending_action
        self.pending_action = None
        try:
            action()
        except Exception as exc:  # noqa: BLE001 - 单个回调失败不应中断界面
            self._error("界面错误", str(exc))

    def toggle_identity(self) -> None:
        if self.identity is not None:
            self.logout()
        else:
            self._login()

    def logout(self) -> None:
        if self.identity is None:
            return
        self._execute(
            ["logout", *self._data_args()],
            on_success=lambda _result: self._after_logout(),
        )

    def _after_logout(self) -> None:
        self._reset_identity_state()
        self._toast("已退出登录")

    def resizeEvent(self, event) -> None:  # noqa: N802
        super().resizeEvent(event)
        if hasattr(self, "toast"):
            self.toast.reposition()

    # ---- 异步执行 -----------------------------------------------------

    def _post(self, callback: Callable[[], None]) -> None:
        if self.closing:
            return
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
        on_failure: Callable[[parsing.Failure, cli.CommandResult], None] | None = None,
    ) -> None:
        if self.busy:
            self._toast("正在执行上一条命令", error=True)
            return
        try:
            self._binary()
        except RuntimeError as exc:
            self._error("chat-deepseek", str(exc))
            return
        self._set_busy(True)

        def work() -> None:
            try:
                result = self._run(args, stdin_text)
            except Exception as exc:  # noqa: BLE001 - 直接呈现子进程错误
                result = cli.CommandResult(tuple(args), 1, "", str(exc))
            self._post(lambda: self._finish(result, on_success, on_failure))

        threading.Thread(target=work).start()

    def _finish(
        self,
        result: cli.CommandResult,
        on_success: Callable[[cli.CommandResult], None] | None,
        on_failure: Callable[[parsing.Failure, cli.CommandResult], None] | None = None,
    ) -> None:
        if self.closing:
            return
        self._set_busy(False)
        if not result.ok:
            if self._binary_missing(result) or self._ssh_failure(result):
                return
            failure = parsing.explain_failure(
                result.stderr, result.stdout, result.args, result.code
            )
            if result.code == 3:
                self._reset_identity_state("登录已失效，请重新登录")
                if on_failure is not None:
                    try:
                        on_failure(failure, result)
                    except Exception as exc:  # noqa: BLE001
                        self._error("界面错误", str(exc))
                return
            if on_failure is not None:
                try:
                    on_failure(failure, result)
                except Exception as exc:  # noqa: BLE001
                    self._error("界面错误", str(exc))
                return
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

    def _resolve_binary(self, binary_dir: str) -> str:
        value = binary_dir.strip()
        if not value:
            raise RuntimeError("请选择 chat-deepseek 所在的二进制目录")
        path = Path(value).expanduser()
        if path.is_file():
            return str(path.resolve())
        candidate = cli.binary_in(path)
        if candidate.is_file():
            return str(candidate.resolve())
        raise RuntimeError(f"目录里没有 {cli.binary_name()}：{path}")

    def _binary(self) -> str:
        if self.remote:
            return ssh_session.remote_binary(self.binary_dir)
        return self._resolve_binary(self.binary_dir)

    def _data_args(self) -> list[str]:
        value = self.data_dir.strip()
        if not value:
            return []
        if not self.remote:
            value = os.path.expanduser(value)
        return ["--data-dir", value]

    def _service_args(self, action: str) -> list[str]:
        return ["service", action]

    def _listen_args(self) -> list[str]:
        value = self.listen.strip()
        return ["--listen", value] if value else []

    def _data_root(self) -> Path:
        if self.remote:
            raise RuntimeError("远端数据目录不在本机")
        value = self.data_dir.strip()
        if value:
            return Path(value).expanduser()
        return Path(self._binary()).parent / "data"

    def _update_data_summary(self) -> None:
        if self.remote:
            self.data_summary.setText(
                "远端路径" if self.data_dir.strip() else "远端默认路径"
            )
            return
        default = Path(self.defaults["data_dir"]).expanduser()
        current = Path(self.data_dir).expanduser() if self.data_dir.strip() else default
        self.data_summary.setText("默认路径" if current == default else "自定义路径")

    def _page_url(self) -> str:
        if self.server_url and not self.remote:
            return self.server_url
        public = self.public_url.strip()
        if public:
            return public.rstrip("/")
        if self.remote:
            # 远端监听地址在远端；GUI 不做端口映射，只能靠 Public URL。
            return ""
        listen = self.listen.strip() or server_config.DEFAULT_LISTEN
        listen = listen.replace("[::]", "127.0.0.1").replace("0.0.0.0", "127.0.0.1")
        if listen.startswith(":"):
            listen = "127.0.0.1" + listen
        if listen.isdigit():
            listen = "127.0.0.1:" + listen
        return "http://" + listen

    # ---- 命令执行 -----------------------------------------------------

    def _command_timeout(self, args: list[str]) -> float:
        # 备份打包可能要几分钟；其余管理命令都是秒级，超时后关掉通道重连。
        return 1800.0 if args and args[0] == "backup" else 120.0

    def _ssh_connection(self) -> ssh_session.SshSession:
        with self._ssh_lock:
            if self.ssh is None:
                self.ssh = ssh_session.SshSession(self.ssh_profile["host"])
            return self.ssh

    def _drop_ssh(self, wait: bool = False) -> None:
        """丢弃常驻通道；默认后台关闭，避免主线程等命令超时。"""
        with self._ssh_lock:
            session = self.ssh
            self.ssh = None
        if session is None:
            return
        if wait:
            try:
                session.close()
            except Exception:  # noqa: BLE001 - 关闭失败不影响界面
                pass
            return

        def close() -> None:
            try:
                session.close()
            except Exception:  # noqa: BLE001 - 关闭失败不影响界面
                pass

        threading.Thread(target=close, daemon=True).start()

    def _run(
        self,
        args: list[str],
        stdin_text: str | None = None,
        *,
        binary: str | None = None,
    ) -> cli.CommandResult:
        timeout = self._command_timeout(args)
        target = binary or self._binary()
        if self.remote:
            return self._ssh_connection().run(
                target, args, stdin_text, timeout
            )
        return cli.run_command(
            target, args, stdin_text, timeout=timeout
        )

    def _binary_missing(self, result: cli.CommandResult) -> bool:
        """远端路径填错时 sh 返回 127；给出比“未知错误”更直接的解释。"""
        if not self.remote or result.code != 127:
            return False
        self._error(
            "找不到 chat-deepseek",
            f"远端没有可执行文件：{self.binary_dir}/chat-deepseek。"
            "请在设置里检查远端二进制目录。",
            result.stderr.strip() or None,
        )
        return True

    def _ssh_failure(self, result: cli.CommandResult) -> bool:
        """SSH 传输层失败（255）统一提示并清掉登录态。"""
        if not self.remote or result.code != ssh_session.TRANSPORT_EXIT:
            return False
        self._drop_ssh()
        self._reset_identity_state("SSH 连接已断开，请重新登录")
        details = result.stderr.strip() or result.stdout.strip()
        self._error(
            "SSH 连接失败",
            f"无法通过 SSH 与 {self.ssh_profile['host']} 通信。请先在终端确认 "
            "`ssh <主机>` 可以直接登录（GUI 只支持密钥/agent 认证）。",
            details or None,
        )
        return True

    def _server_data_dir(self) -> str:
        """服务端数据目录：优先界面上的值，其次 info 返回的实际目录。"""
        value = self.data_dir.strip()
        if value:
            if self.remote:
                return value
            return str(Path(os.path.expanduser(value)).resolve())
        if self.info is not None:
            return self.info.data_dir
        return "" if self.remote else self.defaults["data_dir"]

    def _service_user(self) -> str:
        """systemd 服务所属用户：远端用登录 shell 报回来的用户名。"""
        if self.remote:
            return self.ssh.remote_user if self.ssh is not None else ""
        return service_plan.current_user()

    # ---- 启动 ---------------------------------------------------------

    def bootstrap(self) -> None:
        try:
            self._binary()
        except RuntimeError as exc:
            self._error("chat-deepseek", str(exc))
            return
        # GUI 的登录态绑定在 GUI 进程自己身上；启动时不读取父 shell 的会话，
        # 避免误用（以及在关闭时误退出）终端里的登录。
        self._refresh_data_state()
        self._render_account_state()
        self._update_backup_placeholder()
        self._update_identity_ui()
        self._update_service_ui()
        self._update_buttons()
        if self.config_migration_error:
            self._toast(
                f"config.gui.json 迁移失败：{self.config_migration_error}",
                error=True,
            )
        self._load_info()

    def _load_info(self, then: Callable[[], None] | None = None) -> None:
        """读取 `info`：服务端配置、初始化状态和 systemd 状态。"""
        try:
            self._binary()
        except RuntimeError:
            return
        self.info_generation += 1
        generation = self.info_generation
        args = ["info", *self._data_args()]

        def work() -> None:
            try:
                result = self._run(args)
            except Exception as exc:  # noqa: BLE001 - 直接呈现在提示里
                result = cli.CommandResult(tuple(args), 1, "", str(exc))
            self._post(lambda: self._apply_info(result, generation, then))

        threading.Thread(target=work).start()

    def _apply_info(
        self,
        result: cli.CommandResult,
        generation: int,
        then: Callable[[], None] | None,
    ) -> None:
        if self.closing or generation != self.info_generation:
            return
        if not result.ok:
            if self._binary_missing(result) or self._ssh_failure(result):
                return
            failure = parsing.explain_failure(
                result.stderr, result.stdout, result.args, result.code
            )
            self._error(failure.title, failure.message, failure.details)
            return
        try:
            info = parsing.parse_info(result.stdout)
        except parsing.ParseError as exc:
            failure = parsing.unknown_output(
                "无法识别 chat-deepseek 返回的服务信息。",
                result.stdout,
                result.stderr,
            )
            self._error(
                failure.title,
                failure.message,
                f"{exc}\n\n{failure.details or ''}".strip(),
            )
            return
        self.info = info
        self.data_ready = info.initialized
        self.listen = info.listen
        self.public_url = info.public_url
        self.api_origin = info.api_origin
        self.backup_dir = info.backup_dir
        if self.remote and not self.data_dir.strip():
            # 远端留空表示用 CLI 默认值；info 返回的就是实际目录，填回来便于展示。
            self.data_dir = info.data_dir
            self.ssh_profile["data_dir"] = info.data_dir
        self.service_supported = info.service_supported
        self.service_installed = info.service_installed
        self.service_active = info.service_active
        self.service_enabled = info.service_enabled
        self.service_linger = info.service_linger
        label = f"chat-deepseek {info.version} · {info.os}/{info.arch}"
        if self.remote:
            label += f" · SSH {self.ssh_profile['host']}"
        self.version_label.setText(label)
        self.auto_switch.setEnabled(not self.remote)
        self.auto_switch.setToolTip(
            "远端服务请在浏览器打开 Public URL；GUI 不做端口映射"
            if self.remote
            else ""
        )
        self._update_data_summary()
        self._render_account_state()
        self._update_identity_ui()
        self._update_backup_placeholder()
        self._update_service_ui()
        self._update_buttons()
        self._migrate_legacy_backup_dir()
        if then is not None:
            then()

    def _migrate_legacy_backup_dir(self) -> None:
        """把 v1 config.gui.json 里的备份地址写进 config.json。"""
        legacy = self.legacy_backup_dir
        # v1 的备份地址是本机路径，对远端没有意义。
        if self.remote:
            self.legacy_backup_dir = ""
            return
        if not legacy or self.info is None or self.busy:
            return
        self.legacy_backup_dir = ""
        default_dir = server_config.default_backup_dir(self.info.data_dir)
        if legacy == default_dir or self.info.backup_dir != default_dir:
            return
        self._execute(
            [
                "config",
                "set",
                "backup_dir",
                legacy,
                "--data-dir",
                self.info.data_dir,
            ],
            on_success=lambda _result: self._after_backup_dir_migration(),
            on_failure=lambda failure, _result: self._toast(
                f"旧备份地址未迁移：{failure.message}", error=True
            ),
        )

    def _after_backup_dir_migration(self) -> None:
        self._toast("已把旧备份地址写入 config.json")
        self._load_info()

    def _update_service_ui(self) -> None:
        if not hasattr(self, "status_pill"):
            return
        info = self.info
        self._update_service_strip(info)
        if self.server is not None and self.server.running:
            # serve 日志会更新监听地址和运行状态。
            return
        if info is None:
            self._set_status("尚未启动", "muted", "正在读取服务状态…")
            return
        if info.service_installed:
            if info.service_active:
                self._set_status("运行中", "success", "systemd 服务")
            else:
                self._set_status(
                    "已停止", "muted", "systemd 服务 · 已安装"
                )
            return
        if info.service_supported:
            self._set_status(
                "未运行", "muted", "未安装 systemd 服务（前台运行）"
            )
        else:
            self._set_status(
                "未运行", "muted", "当前平台不支持 systemd 服务（前台运行）"
            )

    def _update_service_strip(self, info: parsing.Info | None) -> None:
        """把 `info` 的 service_* 字段摊在卡片上，别只藏在 tooltip 里。"""
        if not hasattr(self, "info_strip"):
            return
        if info is None or not info.service_installed:
            self.unit_row.hide()
            self.info_strip.setFixedHeight(40)
            return
        self.unit_row.show()
        self.info_strip.setFixedHeight(56)
        self.unit_summary.setText("用户目录（远端）" if self.remote else "用户目录")
        self.enabled_badge.set_state(
            "开机自启" if info.service_enabled else "未设置开机自启",
            "success" if info.service_enabled else "muted",
        )
        self.enabled_badge.setToolTip(
            "已设置开机自启；对应 service install --enable"
            if info.service_enabled
            else "尚未设置开机自启；可在“安装服务”里勾选"
        )
        self.linger_badge.set_state(
            "linger 已开启" if info.service_linger else "linger 未开启",
            "success" if info.service_linger else "warning",
        )
        if self.remote:
            linger = (
                "linger 已开启：断开 SSH 后服务继续运行"
                if info.service_linger
                else "未开启 linger：断开 SSH 后用户级服务会停止。"
                "在远端以该用户执行 loginctl enable-linger <user>"
            )
        else:
            linger = (
                "注销后服务继续运行"
                if info.service_linger
                else "未开启 linger：注销后服务会停止。"
                "以该用户执行 loginctl enable-linger <user>"
            )
        self.linger_badge.setToolTip(linger)
        self.info_strip.setToolTip(
            "systemd 用户服务（远端）" if self.remote else "systemd 用户服务"
        )

    # ---- 账户 ---------------------------------------------------------

    def refresh_accounts(self) -> None:
        if not self._ensure_login():
            return
        self.refresh_users()

    def refresh_users(self) -> None:
        if self.identity is None:
            return
        if self.users_loading or self.busy:
            return
        try:
            self._binary()
        except RuntimeError:
            return
        self.users_loading = True
        self.user_count.setText("…")
        self._update_buttons()
        generation = self.session_generation
        args = ["user", "list", *self._data_args()]

        def work() -> None:
            try:
                result = self._run(args)
            except Exception as exc:  # noqa: BLE001
                result = cli.CommandResult(tuple(args), 1, "", str(exc))
            self._post(lambda: self._apply_users(result, generation))

        threading.Thread(target=work).start()

    def _apply_users(
        self, result: cli.CommandResult, generation: int
    ) -> None:
        if self.closing or generation != self.session_generation:
            return
        self.users_loading = False
        self._update_buttons()
        if not result.ok:
            if self._ssh_failure(result):
                self._run_pending_action()
                return
            if result.code == 3:
                self._reset_identity_state("登录已失效，请重新登录")
                return
            self.user_count.setText("")
            failure = parsing.explain_failure(
                result.stderr, result.stdout, result.args, result.code
            )
            self._error(failure.title, failure.message, failure.details)
            self._run_pending_action()
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
            self._run_pending_action()
            return
        self.users = users
        self.data_ready = True
        self.user_count.setText(str(len(users)))
        self._render_account_state()
        self._run_pending_action()

    def _clear_list(self) -> None:
        while self.list_layout.count():
            item = self.list_layout.takeAt(0)
            widget = item.widget()
            if widget is not None:
                widget.deleteLater()

    def _render_account_state(self) -> None:
        if self.identity is not None:
            self._render_users()
        elif self.data_ready:
            self._render_locked()
        else:
            self._render_setup()

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

        self.setup_button = None
        if self.users:
            for user in self.users:
                row = widgets.UserRow(user)
                row.clicked.connect(self.select_user)
                self.rows[user.username] = row
                self.list_layout.addWidget(row)
            self.list_layout.addStretch(1)
        else:
            self._render_empty(
                widgets.Avatar("+", size=40),
                "还没有账户",
                "点击上方“添加”创建第一个账户。",
            )
        self._paint_selection()

    def _render_locked(self) -> None:
        self.users = []
        self.selected = None
        self._clear_list()
        self.rows.clear()
        self.scroll.setVerticalScrollBarPolicy(
            Qt.ScrollBarPolicy.ScrollBarAlwaysOff
        )
        self.setup_button = None
        self.user_count.setText("")
        self._render_empty(
            widgets.LockGlyph(40),
            "登录后管理账户",
            "Admin 或 Owner 登录后可查看和管理账户。",
            "登录",
            self._login,
        )
        self._paint_selection()

    def _render_setup(self) -> None:
        self.users = []
        self.selected = None
        self._clear_list()
        self.rows.clear()
        self.scroll.setVerticalScrollBarPolicy(
            Qt.ScrollBarPolicy.ScrollBarAlwaysOff
        )
        self.user_count.setText("0")
        self._render_empty(
            widgets.Avatar("+", size=40),
            "还没有账户",
            "创建第一个 Owner，就能在浏览器里登录。",
            "创建 Owner",
            self.setup_owner,
            store_setup=True,
        )
        self._paint_selection()

    def _render_empty(
        self,
        glyph: QWidget,
        title_text: str,
        hint_text: str,
        button_text: str | None = None,
        callback: Callable[[], None] | None = None,
        store_setup: bool = False,
    ) -> None:
        empty = QWidget()
        column = QVBoxLayout(empty)
        # 窗口最小时账户卡片会被服务卡片压缩；这里收紧一点，避免登录按钮被裁掉。
        column.setContentsMargins(14, 4, 14, 4)
        column.setSpacing(5)
        column.addStretch(1)
        column.addWidget(glyph, 0, Qt.AlignmentFlag.AlignHCenter)
        title = widgets.label(title_text, 15, 600)
        title.setAlignment(Qt.AlignmentFlag.AlignCenter)
        column.addWidget(title)
        hint = widgets.label(hint_text, 12, 400, "muted")
        hint.setAlignment(Qt.AlignmentFlag.AlignCenter)
        column.addWidget(hint)
        if button_text and callback is not None:
            button = widgets.button(
                button_text, "primary", height=32, small=True
            )
            button.setMinimumWidth(104)
            button.clicked.connect(lambda _checked=False: callback())
            if store_setup:
                self.setup_button = button
            column.addWidget(button, 0, Qt.AlignmentFlag.AlignHCenter)
        column.addStretch(1)
        self.list_layout.addStretch(1)
        self.list_layout.addWidget(empty)
        self.list_layout.addStretch(1)

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
        if user is not None:
            text = user.username
        elif self.identity is not None:
            text = "选择一个账户"
        elif self.data_ready:
            text = "登录后管理账户"
        else:
            text = "还没有账户"
        self.selection_name.setText(text)
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
        child_running = self.server is not None and self.server.running
        service_running = self.service_installed and self.service_active
        running = child_running or service_running
        busy = self.busy
        identity = self.identity
        logged = identity is not None
        can_prompt = self.data_ready and not logged
        admin = identity is not None and identity.is_admin
        owner = identity is not None and identity.is_owner

        child_mode = not self.service_installed
        self.start_button.setToolTip(
            "以前台进程启动，关闭窗口会停止"
            if child_mode
            else "通过 systemd 用户服务启动"
        )
        self.start_button.setEnabled(not running and not busy)
        self.stop_button.setEnabled(
            running and not self.server_stopping and not busy
        )
        self.restart_button.setVisible(
            self.service_supported and self.service_installed
        )
        self.restart_button.setEnabled(
            not busy and (self.service_active or child_running)
        )
        self.restart_button.setToolTip(
            "重启 systemd 用户服务"
            if self.service_active or child_running
            else "服务没有在运行，先用“启动服务”"
        )
        self.open_button.setEnabled(running and not busy)
        self.install_button.setVisible(
            self.service_supported and not self.service_installed
        )
        self.install_button.setEnabled(not busy and self.data_ready)
        self.install_button.setToolTip(
            "安装 systemd 服务；系统级会给出命令供复制"
            if self.data_ready
            else "先初始化数据目录"
        )
        self.update_button.setVisible(
            self.service_supported and self.service_installed
        )
        self.update_button.setEnabled(not busy and self.data_ready)
        self.update_button.setToolTip(
            "按当前二进制和数据目录重写 systemd 用户 unit"
            if self.data_ready
            else "先初始化数据目录"
        )
        self.uninstall_button.setVisible(
            self.service_supported and self.service_installed
        )
        self.uninstall_button.setEnabled(not busy)
        self.uninstall_button.setToolTip(
            "停止并删除 systemd 用户 unit，不删除数据"
        )
        self.refresh_button.setEnabled(
            not busy and not self.users_loading and (logged or can_prompt)
        )
        self.add_button.setEnabled(
            not busy and not self.users_loading and (admin or can_prompt)
        )
        self.settings_button.setEnabled(not busy)
        self.count_chip.setVisible(identity is not None)

        backup_reason = ""
        if not self.data_ready:
            backup_reason = "数据目录还没有初始化"
        elif logged and not owner:
            backup_reason = "需要 Owner 权限"
        backup_enabled = not busy and not backup_reason
        for button in (
            self.backup_button,
            self.backup_records_button,
            self.backup_as_button,
        ):
            button.setEnabled(backup_enabled)
            button.setToolTip(backup_reason)
        if self.remote:
            self.backup_as_button.setEnabled(False)
            self.backup_as_button.setToolTip(
                "远端备份写到远端配置的备份地址，请用“立即备份”"
            )

        self.help_button.setEnabled(not busy)
        self.identity_button.setEnabled(
            not busy and (logged or self.data_ready)
        )
        if getattr(self, "setup_button", None) is not None:
            self.setup_button.setEnabled(not busy)

        selected = self._selected_user()
        if identity is None:
            reason = "请先登录"
        elif selected is None:
            reason = ""
        else:
            reason = "只能操作权限低于自己的账户"
        checks = [
            (
                identity is not None
                and selected is not None
                and identity.can_reset_password(selected),
                reason,
            ),
            (
                identity is not None
                and selected is not None
                and (
                    identity.can_operate(selected)
                    if selected.disabled
                    else identity.can_disable(selected)
                ),
                "不能操作自己" if selected is not None and selected.username == getattr(identity, "username", "") else reason,
            ),
            (
                owner and selected is not None,
                "需要 Owner 权限" if not owner else reason,
            ),
            (
                owner and selected is not None,
                "需要 Owner 权限" if not owner else reason,
            ),
            (
                identity is not None
                and selected is not None
                and identity.can_delete(selected),
                "不能删除自己" if selected is not None and selected.username == getattr(identity, "username", "") else reason,
            ),
        ]
        for button, (allowed, tip) in zip(self.action_buttons, checks):
            button.setEnabled(not busy and allowed)
            button.setToolTip("" if allowed else tip)
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
            on_success=lambda _result: self._after_setup(name),
        )

    def _after_setup(self, username: str) -> None:
        self.data_ready = True
        self._reset_identity_state()
        self._toast("Owner 已创建，请登录")
        self._load_info()
        self._login(username=username)

    def add_user(self) -> None:
        if not self._ensure_login(self.add_user):
            return
        identity = self.identity
        if identity is None or not identity.is_admin:
            self._toast("需要 Admin 或 Owner 权限", error=True)
            return
        dialog = dialogs.AccountDialog(
            self, "user", allow_groups=identity.is_owner
        )
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
            on_success=lambda _result: self._after_user_changed("账户已创建"),
        )

    def reset_password(self) -> None:
        if self.selected is None or self.identity is None:
            return
        username = self.selected
        dialog = dialogs.PasswordDialog(self, "重置密码", username)
        dialog.exec_modal()
        if dialog.value is None:
            return
        self._execute(
            [
                "user",
                "reset-password",
                *self._data_args(),
                "--password-stdin",
                username,
            ],
            str(dialog.value) + "\n",
            on_success=lambda _result: self._after_password_reset(username),
        )

    def _after_password_reset(self, username: str) -> None:
        # CLI 会吊销目标账户的全部会话；如果改的是自己，当前 GUI 也要重新登录。
        if self.identity is not None and self.identity.username == username:
            self._reset_identity_state()
            self._toast("密码已重置，请重新登录")
            return
        self._toast("密码已重置，旧会话已吊销")
        self.refresh_users()

    def toggle_user(self) -> None:
        user = self._selected_user()
        if user is None or self.identity is None:
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
        if not self._require_owner():
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
        if user is None or self.identity is None:
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
        if self.identity is not None:
            self._refresh_identity()

    # ---- 服务 ---------------------------------------------------------

    def start_server(self) -> None:
        if self.service_installed:
            self._start_systemd_service()
            return
        if self.server is not None and self.server.running:
            return
        self._start_session()

    def _start_session(self) -> None:
        try:
            binary = self._binary()
        except RuntimeError as exc:
            self._error("chat-deepseek", str(exc))
            return
        # 服务端配置由 serve 从 config.json 读取；GUI 只负责通过 config set 写入。
        args = ["serve", "--no-browser", *self._data_args()]

        self.server_url = None
        self.server_address = None
        self.server_tail = []
        self.server_stopping = False
        self.auto_opened = False
        self.server_generation += 1
        generation = self.server_generation
        if self.remote:
            # 远端前台会话和本地一样：GUI 关闭或 SSH 断开就结束，常驻请装服务。
            # 远端命令会先打印自己的 PID，停止时按 PID 发 SIGTERM。
            remote = shlex.join([binary, *args])
            command = ssh_session.serve_command(
                self.ssh_profile["host"], remote
            )
        else:
            command = [binary, *args]
        server = cli.ServerProcess(
            command,
            on_line=lambda line: self._post(
                lambda: self._handle_serve_line(generation, line)
            ),
            on_exit=lambda code: self._post(
                lambda: self._handle_serve_exit(generation, code)
            ),
            marker=ssh_session.SERVE_PID_PREFIX if self.remote else None,
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

    def _service_failure(
        self, failure: parsing.Failure, _result: cli.CommandResult
    ) -> None:
        """服务命令失败时先刷新状态，再把 CLI 的错误提示出来。

        `_execute` 一旦传了 on_failure 就不再弹默认错误框；这里不能只
        reload info，否则更新/启动/停止失败会静默无反馈。
        """
        self._load_info()
        self._error(failure.title, failure.message, failure.details)

    def _start_systemd_service(self) -> None:
        if self.service_active:
            self._toast("服务已经在运行")
            return
        # 已安装 systemd 服务时不再让 GUI 进程抢占同一端口。
        self._stop_foreground_session()
        self.auto_opened = False
        self._execute(
            self._service_args("start"),
            on_success=lambda _result: self._after_service_started(),
            on_failure=self._service_failure,
        )

    def _stop_foreground_session(self, *, wait_remote: bool = True) -> None:
        server = self.server
        if server is None:
            return
        self.server = None
        self.server_url = None
        self.server_address = None
        self.auto_opened = False
        self.server_stopping = True
        if not self.remote:
            if server.running:
                server.stop(wait=wait_remote, timeout=5)
            return

        # 远端：先发 SIGTERM 让远端的 serve 自己退出，ssh 随之结束；发不出去
        # 再兜底结束本地 ssh，免得界面一直卡在“正在停止”。
        def work() -> None:
            pid = server.marker_pid or self._wait_server_pid(server, 5.0)
            if pid is not None and self._send_term(pid):
                return
            if server.running:
                server.stop(wait=True, timeout=5)

        if wait_remote:
            thread = threading.Thread(target=work, daemon=True)
            thread.start()
            # 关窗/切换目标时不能无限等，超时就继续关通道。
            thread.join(timeout=8)
        else:
            threading.Thread(target=work, daemon=True).start()

    def _wait_server_pid(
        self, server: cli.ServerProcess, timeout: float
    ) -> int | None:
        """等远端 serve 报回自己的 PID；刚启动就点停止时那一行可能还在路上。"""
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if server.marker_pid is not None:
                return server.marker_pid
            if not server.running:
                return None
            time.sleep(0.05)
        return server.marker_pid

    def _send_term(self, pid: int) -> bool:
        """结束远端前台会话。

        `ssh -T` 没有分配 pty，客户端退出时 sshd 不会给远端命令发 SIGHUP；
        只结束本地 ssh 会在远端留下继续占端口的进程，所以要按 PID 显式发
        SIGTERM。这条命令走常驻通道，和 serve 自己的 ssh 是两条连接。
        """
        try:
            result = self._ssh_connection().run(
                "kill", ["-TERM", str(pid)], timeout=5
            )
        except Exception:  # noqa: BLE001 - 通道断了就只能交给远端自己退出
            return False
        # kill 返回 1 表示进程已经不在了，同样算结束。
        if result.code in (0, 1):
            return True
        if not self.closing:
            failure = parsing.explain_failure(
                result.stderr, result.stdout, result.args, result.code
            )
            self._post(
                lambda: self._toast(
                    f"没能结束远端进程（PID {pid}）：{failure.message}",
                    error=True,
                )
            )
        return False

    def _after_service_started(self, warnings: str = "") -> None:
        self.service_installed = True
        self.service_active = True
        if warnings and "linger" in warnings:
            self._toast("服务已启动；未开启 linger，注销后会停止")
        elif warnings:
            dialogs.show_message(
                self,
                "服务已启动",
                "systemd 服务已安装并启动。",
                warnings,
            )
        else:
            self._toast("服务已启动")
        self._update_service_ui()
        self._update_buttons()
        self._load_info(then=self._open_after_service_start)

    def _open_after_service_start(self) -> None:
        if (
            self.closing
            or not self.auto_switch.isChecked()
            or self.auto_opened
            or not self.service_active
        ):
            return
        url = self._page_url()
        if not url:
            return
        self.auto_opened = True
        self._open_url(url)

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
            # 远端 serve 打印的是远端 loopback 地址，不能在本机浏览器打开。
            if (
                not self.remote
                and self.auto_switch.isChecked()
                and not self.auto_opened
            ):
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
        self._update_service_ui()
        self._update_buttons()
        if code != 0 and not was_stopping and not self.closing:
            if self.remote and code == ssh_session.TRANSPORT_EXIT:
                self._error(
                    "SSH 连接失败",
                    "远端 serve 会话已断开；远程常驻请安装 systemd 服务。",
                    "\n".join(self.server_tail).strip() or None,
                )
            else:
                failure = parsing.explain_serve_failure(self.server_tail, code)
                self._error(failure.title, failure.message, failure.details)
        elif was_stopping:
            self._toast("服务已停止")

    def stop_server(self) -> None:
        if self.service_installed:
            self._stop_systemd_service()
            return
        if self.server is None or not self.server.running:
            self._toast("服务没有在运行", error=True)
            return
        self.server_stopping = True
        self._set_status("正在停止…", "warning", "正在关闭服务")
        self._update_buttons()
        self._stop_foreground_session(wait_remote=False)

    def _stop_systemd_service(self) -> None:
        child_running = self.server is not None and self.server.running
        if not self.service_active and not child_running:
            self._toast("服务没有在运行", error=True)
            return
        self._stop_foreground_session()
        self._set_status("正在停止…", "warning", "正在停止 systemd 服务")
        self._update_buttons()
        self._execute(
            self._service_args("stop"),
            on_success=lambda _result: self._after_service_stopped(),
            on_failure=self._service_failure,
        )

    def _after_service_stopped(self) -> None:
        self.server_url = None
        self.service_active = False
        self._toast("服务已停止")
        self._load_info()

    def restart_server(self) -> None:
        if not self.service_installed:
            self._toast("先安装 systemd 服务", error=True)
            return
        child_running = self.server is not None and self.server.running
        if not self.service_active and not child_running:
            self._toast("服务没有在运行", error=True)
            return
        self._stop_foreground_session()
        self._set_status("正在重启…", "warning", "正在重启 systemd 服务")
        self._update_buttons()
        self._execute(
            self._service_args("restart"),
            on_success=lambda _result: self._after_service_restarted(),
            on_failure=self._service_failure,
        )

    def _after_service_restarted(self, warnings: str = "") -> None:
        self.service_installed = True
        self.service_active = True
        if warnings and "linger" in warnings:
            self._toast("服务已重启；未开启 linger，注销后会停止")
        elif warnings:
            dialogs.show_message(
                self,
                "服务已重启",
                "systemd 服务已重启。",
                warnings,
            )
        else:
            self._toast("服务已重启")
        self._update_service_ui()
        self._update_buttons()
        self._load_info()

    def install_service(self) -> None:
        if not self.service_supported:
            self._toast("当前平台不支持 systemd 服务", error=True)
            return
        if not self.data_ready:
            self._toast("先初始化数据目录，再安装服务", error=True)
            return
        try:
            binary = self._binary()
        except RuntimeError as exc:
            self._error("chat-deepseek", str(exc))
            return
        raw_data_dir = self._server_data_dir()
        if not raw_data_dir:
            self._error("chat-deepseek", "先读取远端数据目录")
            return
        data_dir = raw_data_dir
        dialog = dialogs.InstallServiceDialog(
            self,
            binary=binary,
            data_dir=data_dir,
            linger=self.service_linger,
            username=self._service_user(),
            host=self.ssh_profile["host"] if self.remote else "",
        )
        dialog.exec_modal()
        value = dialog.value
        dialog.deleteLater()
        if not isinstance(value, dict):
            return
        options = value
        self._execute(
            service_plan.install_args(
                data_dir,
                enable=bool(options.get("enable")),
                force=bool(options.get("force")),
            ),
            on_success=lambda result: self._after_service_install(result, options),
        )

    def _after_service_install(
        self, result: cli.CommandResult, options: dict[str, object]
    ) -> None:
        warnings = result.stderr.strip()
        # 安装成功后立刻更新本地状态，避免 info 返回前又走前台启动。
        self.info = None
        self.service_installed = True
        self.service_active = False
        self.service_enabled = bool(options.get("enable"))
        self._update_service_ui()
        self._update_buttons()
        if bool(options.get("start")):
            self._stop_foreground_session()
            self._execute(
                self._service_args("start"),
                on_success=lambda _result: self._after_service_started(warnings),
                on_failure=self._service_failure,
            )
            return
        message = "已写入 systemd unit 并重新加载；服务尚未启动。"
        if warnings:
            dialogs.show_message(self, "服务已安装", message, warnings)
        else:
            self._toast("服务已安装，尚未启动")
        self._load_info()

    def update_service(self) -> None:
        if not self.service_installed:
            return
        if not self.data_ready:
            self._toast("先初始化数据目录，再更新服务", error=True)
            return
        try:
            binary = self._binary()
        except RuntimeError as exc:
            self._error("chat-deepseek", str(exc))
            return
        raw_data_dir = self._server_data_dir()
        if not raw_data_dir:
            self._error("chat-deepseek", "先读取远端数据目录")
            return
        data_dir = raw_data_dir
        dialog = dialogs.InstallServiceDialog(
            self,
            binary=binary,
            data_dir=data_dir,
            linger=self.service_linger,
            username=self._service_user(),
            host=self.ssh_profile["host"] if self.remote else "",
            installed=True,
            enabled=self.service_enabled,
            active=self.service_active,
        )
        dialog.exec_modal()
        value = dialog.value
        dialog.deleteLater()
        if not isinstance(value, dict):
            return
        options = value
        target_enabled = bool(options.get("enable"))
        self._execute(
            service_plan.update_args(
                data_dir,
                enable=target_enabled,
            ),
            on_success=lambda result: self._after_service_update(result, options),
            on_failure=self._service_failure,
        )

    def _after_service_update(
        self, result: cli.CommandResult, options: dict[str, object]
    ) -> None:
        warnings = result.stderr.strip()
        self.service_installed = True
        self.service_enabled = bool(options.get("enable"))
        self._update_service_ui()
        self._update_buttons()
        if options.get("start"):
            self._stop_foreground_session()
            # 不根据缓存的 service_active 分支：GUI 打开后服务可能被外部启停，
            # 缓存为 false 时 `start` 对已在运行的 unit 是空操作，会继续跑旧进程。
            # `restart` 在服务停止时同样会启动它，因此这里统一用 restart。
            self._execute(
                self._service_args("restart"),
                on_success=lambda _result: self._after_service_restarted(warnings),
                on_failure=self._service_failure,
            )
            return
        message = "systemd unit 已更新；未启动或重启服务。"
        if warnings:
            dialogs.show_message(self, "服务已更新", message, warnings)
        else:
            self._toast(message)
        self._load_info()

    def uninstall_service(self) -> None:
        if not dialogs.ask_confirm(
            self,
            "卸载服务",
            "停止并删除 systemd 用户服务（unit 文件），同时取消开机自启？"
            "\n\n数据目录不会被删除；linger 设置保持不变。",
            danger=True,
        ):
            return
        self._execute(
            self._service_args("uninstall"),
            on_success=lambda _result: self._after_service_uninstall(),
            on_failure=self._service_failure,
        )

    def _after_service_uninstall(self) -> None:
        self.server_url = None
        self.info = None
        self.service_installed = False
        self.service_active = False
        self.service_enabled = False
        self._update_service_ui()
        self._update_buttons()
        self._toast("服务已卸载，数据目录未改动")
        self._load_info()

    def open_page(self) -> None:
        running = self.server is not None and self.server.running
        if not running and not (self.service_installed and self.service_active):
            self._toast("服务没有在运行", error=True)
            return
        url = self._page_url()
        if not url:
            self._toast(
                "远端服务需要先配置 Public URL，GUI 不做端口映射", error=True
            )
            return
        self._open_url(url)

    def _open_url(self, url: str) -> None:
        if not url:
            self._toast("没有可打开的地址", error=True)
            return
        QDesktopServices.openUrl(QUrl(url))
        self._toast(f"已打开 {url}")

    # ---- 备份与帮助 ---------------------------------------------------

    def backup_default(self) -> None:
        if not self._ensure_login(self.backup_default):
            return
        if not self._require_owner():
            return
        self._execute(
            ["backup", *self._data_args()],
            on_success=self._after_backup,
        )

    def backup_as(self) -> None:
        if self.remote:
            self._toast(
                "远端备份写到远端配置的备份地址，请用“立即备份”", error=True
            )
            return
        if not self._ensure_login(self.backup_as):
            return
        if not self._require_owner():
            return
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
            ],
            on_success=self._after_backup,
        )

    def refresh_backups(self) -> None:
        if self.identity is None or not self.identity.is_owner:
            self._update_backup_placeholder()
            return
        if self.backups_loading or self.closing:
            return
        try:
            self._binary()
        except RuntimeError:
            return
        self.backups_loading = True
        generation = self.session_generation
        args = ["backup", "list", *self._data_args()]

        def work() -> None:
            try:
                result = self._run(args)
            except Exception as exc:  # noqa: BLE001 - 直接呈现在信息栏
                result = cli.CommandResult(tuple(args), 1, "", str(exc))
            self._post(
                lambda: self._apply_backup_summary(result, generation)
            )

        threading.Thread(target=work).start()

    def _apply_backup_summary(
        self, result: cli.CommandResult, generation: int
    ) -> None:
        if self.closing or generation != self.session_generation:
            return
        self.backups_loading = False
        if not result.ok:
            if self._ssh_failure(result):
                self._run_pending_action()
                return
            if result.code == 3:
                self._reset_identity_state("登录已失效，请重新登录")
                return
            failure = parsing.explain_failure(
                result.stderr, result.stdout, result.args, result.code
            )
            self.backups = []
            self._update_backup_placeholder()
            if self.identity is not None and self.identity.is_owner:
                self.backup_label.setText("无法读取备份记录")
                self.backup_label.setToolTip(failure.message)
            self._run_pending_action()
            return
        try:
            archives = parsing.parse_backup_list(result.stdout)
        except parsing.ParseError:
            self.backups = []
            self.backup_label.setText("无法读取备份记录")
            self.backup_label.setToolTip("备份列表格式无法识别")
            self._run_pending_action()
            return
        self.backups = archives
        self._update_backup_placeholder()
        self._run_pending_action()

    def _update_backup_placeholder(self) -> None:
        if self.identity is None:
            self.backup_label.setText("登录后查看备份记录")
            self.backup_label.setToolTip("登录 Owner 后可以查看和创建备份")
            return
        if not self.identity.is_owner:
            self.backup_label.setText("需要 Owner 登录才能查看备份")
            self.backup_label.setToolTip("备份操作只对 Owner 开放")
            return
        if not self.backups:
            self.backup_label.setText("还没有备份记录")
            self.backup_label.setToolTip("")
            return
        latest = self.backups[0]
        self.backup_label.setText(
            f"最近备份：{latest.time:%Y-%m-%d %H:%M:%S}"
            f"（{latest.size}）"
        )
        self.backup_label.setToolTip("")

    def manage_backups(self) -> None:
        if not self._ensure_login(self.manage_backups):
            return
        if not self._require_owner():
            return
        self._execute(
            ["backup", "list", *self._data_args()],
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
        self._update_backup_placeholder()
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
                name,
            ],
            on_success=lambda _result: self._after_restore(name),
        )

    def _after_restore(self, name: str) -> None:
        # restore 会覆盖 chat.db，并主动清掉当前 GUI 的登录态；必须重新登录。
        self.selected = None
        self.backups = []
        self._refresh_data_state()
        self._load_info()
        stamp = parsing.parse_backup_time(name)
        label = stamp.strftime("%Y-%m-%d %H:%M:%S") if stamp else name
        self._reset_identity_state()
        self._toast(f"备份已恢复（{label}），请重新登录并启动服务")

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
        if not self.remote and target.is_file():
            human = parsing.format_size(target.stat().st_size)
            text = f"最近备份：{label}（{human}）"
        else:
            text = f"最近备份：{label}"
        self.backup_label.setText(text)
        self.backup_label.setToolTip("")
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
        """打开设置；远端还没读到 config.json 时先读一次再弹表单。"""
        if self.remote and self.info is None and not self.settings_pending:
            # 表单里的值只能来自目标主机的 config.json，先读一次，避免把本机或
            # 上一个目标的值填进来。
            self.settings_pending = True
            self._toast("正在读取远端配置…")
            self._load_info(then=self._open_settings_after_info)
            # 读失败时 _load_info 不会再回调；兜底照常打开，否则改不了路径。
            QTimer.singleShot(2500, self._open_settings_after_info)
            return
        self._open_settings()

    def _open_settings_after_info(self) -> None:
        if not self.settings_pending or self.closing:
            return
        self._open_settings()

    def _lookup_server_config(
        self,
        dialog: dialogs.SettingsDialog,
        connection: str,
        target: dict[str, str],
        generation: int,
    ) -> None:
        """读取所选主机自己的 config.json，异步回填设置表单。

        备份地址、监听地址、Public URL、API 地址都存在各自数据目录的
        config.json 里；切换连接方式时必须按目标主机重新读。
        """

        def work() -> None:
            values, error = self._read_server_config(connection, target)
            self._post(
                lambda: self._apply_server(
                    dialog, connection, generation, values, error
                )
            )

        threading.Thread(target=work, daemon=True).start()

    def _apply_server(
        self,
        dialog: dialogs.SettingsDialog,
        connection: str,
        generation: int,
        values: dict[str, str] | None,
        error: str,
    ) -> None:
        try:
            dialog.apply_server(connection, generation, values, error)
        except RuntimeError:
            # 读回来之前对话框已经关掉了。
            pass

    def _read_server_config(
        self, connection: str, target: dict[str, str]
    ) -> tuple[dict[str, str] | None, str]:
        args = ["info"]
        data_dir = target.get("data_dir", "").strip()
        if data_dir:
            args += ["--data-dir", data_dir]
        try:
            if connection == "local":
                command = [
                    self._resolve_binary(target.get("binary_dir", "")),
                    *args,
                ]
            else:
                host = target.get("host", "").strip()
                if not host:
                    return None, "请先填写 SSH 主机"
                binary = ssh_session.remote_binary(target.get("binary_dir", ""))
                command = ssh_session.ssh_command(
                    host, shlex.join([binary, *args])
                )
        except RuntimeError as exc:
            return None, str(exc)
        try:
            result = cli.run_command(command[0], command[1:], timeout=20)
        except Exception as exc:  # noqa: BLE001 - 超时等直接作为提示
            return None, str(exc)
        if not result.ok:
            failure = parsing.explain_failure(
                result.stderr, result.stdout, result.args, result.code
            )
            return None, failure.message
        try:
            info = parsing.parse_info(result.stdout)
        except parsing.ParseError:
            return None, "无法识别 chat-deepseek 返回的服务信息"
        return {
            "backup_dir": info.backup_dir,
            "listen": info.listen,
            "public_url": info.public_url,
            "api_origin": info.api_origin,
        }, ""

    def _open_settings(self) -> None:
        self.settings_pending = False
        dialog = dialogs.SettingsDialog(
            self,
            connection=self.connection,
            local=self.local_profile,
            ssh=self.ssh_profile,
            backup_dir=self.backup_dir,
            listen=self.listen,
            public_url=self.public_url,
            api_origin=self.api_origin,
            defaults=self.defaults,
            server_lookup=self._lookup_server_config,
        )
        dialog.exec_modal()
        if not isinstance(dialog.value, dict):
            return
        value = dialog.value
        connection = str(value["connection"])
        local = dict(value["local"])
        ssh_values = dict(value["ssh"])
        local["username"] = self.local_profile["username"]
        ssh_values["username"] = self.ssh_profile["username"]
        backup_dir = str(value["backup_dir"])
        listen = str(value["listen"])
        public_url = str(value["public_url"])
        api_origin = str(value["api_origin"])
        if self.busy:
            self._toast("正在执行上一条命令", error=True)
            return

        target_changed = connection != self.connection or (
            connection == "ssh"
            and ssh_values["host"] != self.ssh_profile["host"]
        )
        try:
            if connection == "local":
                binary = self._resolve_binary(local["binary_dir"])
            else:
                binary = ssh_session.remote_binary(ssh_values["binary_dir"])
        except RuntimeError as exc:
            self._error("chat-deepseek", str(exc))
            return

        if target_changed:
            data_changed = False
        elif connection == "local":
            data_changed = (
                Path(local["data_dir"]).expanduser()
                != Path(self.data_dir).expanduser()
            )
        else:
            desired = ssh_values["data_dir"].strip()
            if not desired and self.info is not None:
                desired = self.info.data_dir
            data_changed = desired != self.data_dir.strip()

        # 换连接/主机时，界面上的服务端配置还是旧目标的，不能写到新目标上。
        changed = (
            []
            if target_changed
            else self._settings_changes(
                data_changed, listen, public_url, api_origin, backup_dir
            )
        )
        old_binary = self.binary_dir
        old_data = self.data_dir
        had_identity = self.identity is not None
        config_dir = local["data_dir"] if connection == "local" else ssh_values["data_dir"]

        self._set_busy(True)

        def work() -> None:
            for key, value_text in changed:
                args = ["config", "set", key, value_text]
                if config_dir.strip():
                    args += ["--data-dir", config_dir]
                try:
                    result = self._run(args, binary=binary)
                except Exception as exc:  # noqa: BLE001 - 直接呈现在提示里
                    result = cli.CommandResult(tuple(args), 1, "", str(exc))
                if not result.ok:
                    self._post(lambda r=result: self._settings_failed(r))
                    return
            self._post(
                lambda: self._settings_saved(
                    value,
                    target_changed,
                    data_changed,
                    had_identity,
                    old_binary,
                    old_data,
                )
            )

        threading.Thread(target=work).start()

    def _logout_target(
        self, remote: bool, host: str, binary_dir: str, data_dir: str
    ) -> None:
        """显式退出旧目标，清掉它数据库里的 auth_sessions 行。

        切换连接方式/主机/数据目录之后 GUI 已经拿不到旧目标的登录态，不主动
        logout 就会在它的 auth_sessions 里留一行，直到 15 分钟过期。失败不
        阻塞设置保存，等过期即可。
        """
        args = ["logout"]
        data = data_dir.strip()
        if data:
            args += ["--data-dir", data if remote else os.path.expanduser(data)]
        try:
            if remote:
                # 远端登录态绑在常驻通道那个 shell 上；另开一条 ssh 的 shell
                # 找不到会话文件，只能等过期，所以必须走原通道。
                session = self.ssh
                if session is None or not session.connected:
                    return
                session.run(
                    ssh_session.remote_binary(binary_dir), args, timeout=5
                )
                return
            path = Path(binary_dir).expanduser()
            executable = path if path.is_file() else cli.binary_in(path)
            if executable.is_file():
                cli.run_command(str(executable), args, timeout=5)
        except Exception:  # noqa: BLE001 - 登出失败不阻塞设置保存
            pass

    def _settings_changes(
        self,
        data_changed: bool,
        listen: str,
        public_url: str,
        api_origin: str,
        backup_dir: str,
    ) -> list[tuple[str, str]]:
        """只写变化的项；换数据目录时把全部配置复制到新目录。"""
        current = {
            "listen": self.listen,
            "public_url": self.public_url,
            "api_origin": self.api_origin,
            "backup_dir": self.backup_dir,
        }
        desired = {
            "listen": listen,
            "public_url": public_url,
            "api_origin": api_origin,
            "backup_dir": backup_dir,
        }
        return [
            (key, desired[key])
            for key in current
            if data_changed or desired[key] != current[key]
        ]

    def _settings_failed(self, result: cli.CommandResult) -> None:
        self._set_busy(False)
        if self._ssh_failure(result):
            return
        failure = parsing.explain_failure(
            result.stderr, result.stdout, result.args, result.code
        )
        details = failure.details
        note = "出错前已经写入的配置项会保留在 config.json。"
        details = f"{details}\n\n{note}" if details else note
        self._error(failure.title, failure.message, details)

    def _settings_saved(
        self,
        value: dict[str, object],
        target_changed: bool,
        data_changed: bool,
        had_identity: bool,
        old_binary: str,
        old_data: str,
    ) -> None:
        self._set_busy(False)
        connection = str(value["connection"])
        local = dict(value["local"])  # type: ignore[arg-type]
        ssh_values = dict(value["ssh"])  # type: ignore[arg-type]
        local["username"] = self.local_profile["username"]
        ssh_values["username"] = self.ssh_profile["username"]
        listen = str(value["listen"])
        public_url = str(value["public_url"])
        api_origin = str(value["api_origin"])
        backup_dir = str(value["backup_dir"])
        try:
            gui_config.save(
                self.gui_config_path,
                gui_config.Config(
                    connection=connection,
                    local=gui_config.LocalConfig(**local),
                    ssh=gui_config.SshConfig(**ssh_values),
                ),
            )
        except OSError as exc:
            self._error("保存设置失败", f"无法写入 config.gui.json：{exc}")
            return

        was_remote = self.remote
        old_host = self.ssh_profile["host"]
        if had_identity and (target_changed or data_changed):
            # GUI 切走以后就拿不到旧目标的登录态了，不 logout 会在它的
            # auth_sessions 里留一行，等过期才清掉。
            self._logout_target(was_remote, old_host, old_binary, old_data)
        self.local_profile = local
        self.ssh_profile = ssh_values
        if target_changed:
            self._stop_foreground_session()
            self._drop_ssh()
            self.users = []
            self.selected = None
            self.backups = []
        elif data_changed and was_remote:
            # 远端登录态绑定旧数据目录；关掉通道，下次命令重新登录。
            self._drop_ssh()
        stale = target_changed or data_changed
        if stale:
            self.info = None
            self.data_ready = False
            self.service_supported = False
            self.service_installed = False
            self.service_active = False
            self.service_enabled = False
            self.service_linger = False

        self.connection = connection
        self.binary_dir = (
            local["binary_dir"]
            if connection == "local"
            else ssh_values["binary_dir"]
        )
        self.data_dir = (
            local["data_dir"] if connection == "local" else ssh_values["data_dir"]
        )
        self.last_username = (
            local["username"]
            if connection == "local"
            else ssh_values["username"]
        )
        self.listen = listen
        self.public_url = public_url
        self.api_origin = api_origin
        if connection == "local" and not stale:
            self.defaults = server_config.defaults(Path(local["binary_dir"]))
            resolved_data = local["data_dir"] or str(
                server_config.default_data_dir(local["binary_dir"])
            )
            self.backup_dir = backup_dir or server_config.default_backup_dir(
                resolved_data
            )
        elif connection == "local":
            self.defaults = server_config.defaults(Path(local["binary_dir"]))
            self.backup_dir = ""
        else:
            # 远端备份地址只认该主机 config.json：对话框里的值可能还是上一个
            # 目标或本机的，等 _load_info 读回来。
            self.backup_dir = ""
        self._update_data_summary()
        self._refresh_data_state()

        if stale:
            self._reset_identity_state()
        elif self.identity is not None:
            self._refresh_identity()
        else:
            self._render_account_state()
            self._update_identity_ui()
            self._update_backup_placeholder()
            self._update_buttons()

        running = (
            self.server is not None and self.server.running
        ) or (self.service_installed and self.service_active)
        message = "设置已保存；服务重启后生效" if running else "设置已保存"
        if target_changed:
            message += "；连接已切换，请重新登录"
        elif data_changed:
            message += "；数据目录已切换，请重新登录"
        self._toast(message)
        self._load_info()

    # ---- 关闭 ---------------------------------------------------------

    def closeEvent(self, event) -> None:  # noqa: N802
        if self.server is not None and self.server.running:
            if not dialogs.ask_confirm(
                self, "退出", "服务还在运行。停止服务并退出？", danger=True
            ):
                event.ignore()
                return
            self.server_stopping = True
            self._stop_foreground_session()
        self.closing = True
        self._drain_timer.stop()
        self._identity_timer.stop()
        if self.identity is not None:
            try:
                if self.remote:
                    self._ssh_connection().run(
                        self._binary(),
                        ["logout", *self._data_args()],
                        timeout=5,
                    )
                else:
                    cli.run_command(
                        self._binary(),
                        ["logout", *self._data_args()],
                        timeout=5,
                    )
            except Exception:  # noqa: BLE001 - 关闭时登出失败不阻塞退出
                pass
        self._drop_ssh(wait=True)
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
