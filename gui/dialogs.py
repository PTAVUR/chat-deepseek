"""Qt 对话框：账户、密码、组、确认、消息、设置与服务安装。"""

from __future__ import annotations

import posixpath
from collections.abc import Callable
from pathlib import Path

from PySide6.QtCore import Qt
from PySide6.QtGui import QFontDatabase, QGuiApplication
from PySide6.QtWidgets import (
    QButtonGroup,
    QDialog,
    QFileDialog,
    QFrame,
    QGridLayout,
    QHBoxLayout,
    QLabel,
    QScrollArea,
    QVBoxLayout,
    QWidget,
)

import cli
import parsing
import server_config
import service_plan
import widgets

GROUPS = ("User", "Admin", "Owner")


def _field(placeholder: str = "", password: bool = False) -> widgets.Field:
    return widgets.Field(placeholder, password=password)


def _browse(parent: QWidget, title: str, current: str) -> str | None:
    start = current.strip() or str(Path.home())
    path = QFileDialog.getExistingDirectory(parent, title, start)
    return path or None


def choose_directory(parent: QWidget, title: str, current: str) -> str | None:
    return _browse(parent, title, current)


def choose_save_path(
    parent: QWidget, title: str, directory: str, filename: str
) -> str | None:
    path, _ = QFileDialog.getSaveFileName(
        parent, title, str(Path(directory) / filename), "ZIP 归档 (*.zip)"
    )
    return path or None


class BaseDialog(QDialog):
    def __init__(
        self,
        parent: QWidget,
        title: str,
        width: int,
        height: int | None = None,
    ) -> None:
        super().__init__(parent)
        self.value: object = None
        self.setWindowTitle(title)
        self.setObjectName("sheet")
        self.setModal(True)
        self.setWindowFlag(Qt.WindowType.WindowContextHelpButtonHint, False)

        self._root = QVBoxLayout(self)
        self._root.setContentsMargins(24, 16, 24, 20)
        self._root.setSpacing(12)

        if height is None:
            self.setMinimumWidth(width)
        else:
            self.setFixedSize(width, max(160, height - 40))

    def _description(self, description: str) -> None:
        text = widgets.label(description, 12, 400, "muted")
        text.setWordWrap(True)
        self._root.addWidget(text)

    def _error_label(self) -> QLabel:
        error = widgets.label("", 12, 400, "danger")
        error.setWordWrap(True)
        error.hide()
        self._error = error
        return error

    def _show_error(self, message: str) -> None:
        self._error.setText(message)
        self._error.show()

    def _buttons(
        self,
        primary: str,
        on_primary,
        cancel: str = "取消",
        danger: bool = False,
    ) -> None:
        row = QHBoxLayout()
        row.setContentsMargins(0, 4, 0, 0)
        row.setSpacing(8)
        row.addStretch(1)
        cancel_button = widgets.button(cancel, "secondary", height=36)
        cancel_button.setMinimumWidth(88)
        cancel_button.clicked.connect(self.reject)
        row.addWidget(cancel_button)
        self.cancel_button = cancel_button
        primary_button = widgets.button(
            primary, "danger" if danger else "primary", height=36
        )
        primary_button.setMinimumWidth(92)
        primary_button.setDefault(True)
        primary_button.clicked.connect(on_primary)
        row.addWidget(primary_button)
        self.primary_button = primary_button
        self._root.addLayout(row)
        return primary_button

    def exec_modal(self) -> int:
        self.adjustSize()
        parent = self.parentWidget()
        if parent is not None:
            geo = parent.window().frameGeometry()
            frame = self.frameGeometry()
            self.move(
                geo.center().x() - frame.width() // 2,
                geo.center().y() - frame.height() // 2,
            )
        return self.exec()


class PasswordDialog(BaseDialog):
    def __init__(self, parent: QWidget, title: str, username: str | None = None) -> None:
        super().__init__(parent, title, 460, 300)
        self._description(
            f"为 {username} 设置新密码，至少 8 位。" if username else "至少 8 位。"
        )
        self.first = _field("新密码", password=True)
        self.second = _field("再次输入", password=True)
        self._root.addWidget(self.first)
        self._root.addWidget(self.second)
        self._root.addWidget(self._error_label())
        self._root.addStretch(1)
        self._buttons("保存", self._ok)
        self.first.returnPressed.connect(self.second.setFocus)
        self.second.returnPressed.connect(self._ok)
        self.first.setFocus()

    def _ok(self) -> None:
        password = self.first.text()
        if not 8 <= len(password) <= 1024:
            self._show_error("密码长度需在 8–1024 位之间")
            return
        if password != self.second.text():
            self._show_error("两次输入的密码不一致")
            return
        self.value = password
        self.accept()


class LoginDialog(BaseDialog):
    def __init__(
        self,
        parent: QWidget,
        username: str = "",
        error: str | None = None,
    ) -> None:
        super().__init__(parent, "登录", 460, 380)
        self._description(
            "使用 Admin 或 Owner 账户登录。登录只在本窗口有效，15 分钟后自动退出。"
        )
        self.username = _field("用户名")
        self.username.setText(username)
        self.password = _field("密码", password=True)
        self._root.addWidget(self.username)
        self._root.addWidget(self.password)
        self._root.addWidget(self._error_label())
        if error:
            self._show_error(error)
        self._root.addStretch(1)
        self._buttons("登录", self._ok)
        self.username.returnPressed.connect(self.password.setFocus)
        self.password.returnPressed.connect(self._ok)
        if username:
            self.password.setFocus()
        else:
            self.username.setFocus()

    def _ok(self) -> None:
        username = self.username.text().strip()
        password = self.password.text()
        if not username:
            self._show_error("请输入用户名")
            self.username.setFocus()
            return
        if not password:
            self._show_error("请输入密码")
            self.password.setFocus()
            return
        self.value = (username, password)
        self.accept()


class AccountDialog(BaseDialog):
    def __init__(
        self,
        parent: QWidget,
        mode: str,
        allow_groups: bool = True,
    ) -> None:
        title = "创建 Owner" if mode == "owner" else "添加用户"
        super().__init__(parent, title, 480, 350 if mode == "owner" else 420)
        self.mode = mode
        self.allow_groups = allow_groups
        if mode == "owner":
            description = "第一个账户会成为 Owner。"
        elif allow_groups:
            description = "新用户默认属于 User 组，可附加 Admin 或 Owner。"
        else:
            description = "新用户默认属于 User 组；Admin 不能指定附加组。"
        self._description(description)
        self.username = _field("用户名")
        self.password = _field("密码", password=True)
        self.confirm = _field("再次输入密码", password=True)
        for entry in (self.username, self.password, self.confirm):
            self._root.addWidget(entry)
        self.admin: widgets.CheckBox | None = None
        self.owner: widgets.CheckBox | None = None
        if mode == "user" and allow_groups:
            self.admin = widgets.CheckBox("Admin")
            self.owner = widgets.CheckBox("Owner")
            row = QHBoxLayout()
            row.setSpacing(18)
            caption = widgets.label("附加组", 11, 500, "muted")
            row.addWidget(caption)
            row.addSpacing(4)
            row.addWidget(self.admin)
            row.addWidget(self.owner)
            row.addStretch(1)
            holder = QWidget()
            holder.setLayout(row)
            row.setContentsMargins(0, 0, 0, 0)
            self._root.addWidget(holder)
        else:
            self._root.addSpacing(2)
        self._root.addWidget(self._error_label())
        self._root.addStretch(1)
        self._buttons("创建", self._ok)
        for entry in (self.username, self.password, self.confirm):
            entry.returnPressed.connect(self._ok)
        self.username.setFocus()

    def _ok(self) -> None:
        username = self.username.text().strip()
        password = self.password.text()
        if not username:
            self._show_error("用户名不能为空")
            return
        if not 8 <= len(password) <= 1024:
            self._show_error("密码长度需在 8–1024 位之间")
            return
        if password != self.confirm.text():
            self._show_error("两次输入的密码不一致")
            return
        groups: list[str] = []
        if self.mode == "user":
            if self.admin is not None and self.admin.isChecked():
                groups.append("Admin")
            if self.owner is not None and self.owner.isChecked():
                groups.append("Owner")
        self.value = (username, password, tuple(groups))
        self.accept()


class GroupDialog(BaseDialog):
    def __init__(self, parent: QWidget, username: str, add: bool) -> None:
        title = "授予组" if add else "撤销组"
        super().__init__(parent, title, 420, 330)
        self._description(f"{'授予' if add else '撤销'} {username} 的组")
        self.group = QButtonGroup(self)
        self.group.setExclusive(True)
        for index, name in enumerate(GROUPS):
            row = widgets.ChoiceRow(name)
            self.group.addButton(row, index)
            self._root.addWidget(row)
            if name == "Admin":
                row.setChecked(True)
        self._root.addWidget(self._error_label())
        self._root.addStretch(1)
        self._buttons("确定", self._ok)
        self._error.hide()

    def _ok(self) -> None:
        checked = self.group.checkedButton()
        if checked is None:
            self._show_error("请选择一个组")
            return
        self.value = checked.text()
        self.accept()


class ConfirmDialog(BaseDialog):
    def __init__(
        self,
        parent: QWidget,
        title: str,
        message: str,
        danger: bool = False,
    ) -> None:
        super().__init__(parent, title, 440, 220)
        text = widgets.label(message, 13)
        text.setWordWrap(True)
        self._root.addWidget(text)
        self._root.addStretch(1)
        self._buttons("确定", self._ok, danger=danger)

    def _ok(self) -> None:
        self.value = True
        self.accept()


class MessageDialog(BaseDialog):
    def __init__(
        self,
        parent: QWidget,
        title: str,
        message: str,
        details: str | None = None,
    ) -> None:
        super().__init__(parent, title, 560, 400 if details else 250)
        text = widgets.label(message, 13)
        text.setWordWrap(True)
        self._root.addWidget(text)
        if details:
            box = widgets.TextBox()
            box.setPlainText(details)
            box.edit.setFont(
                QFontDatabase.systemFont(QFontDatabase.SystemFont.FixedFont)
            )
            self._root.addWidget(box, 1)
        else:
            self._root.addStretch(1)
        row = QHBoxLayout()
        row.addStretch(1)
        close = widgets.button("关闭", "secondary", height=36)
        close.setMinimumWidth(88)
        close.setDefault(True)
        close.clicked.connect(self.accept)
        row.addWidget(close)
        self._root.addLayout(row)


class BackupDialog(BaseDialog):
    def __init__(
        self,
        parent: QWidget,
        backup_dir: str,
        archives: list[parsing.Backup],
    ) -> None:
        super().__init__(parent, "备份记录", 640, 500)
        self.selected: str | None = None
        self._description(f"备份地址：{backup_dir}")
        hint = widgets.label(
            "恢复会覆盖 chat.db、config.json、master.key 和 files/，请先停止服务。",
            12,
            400,
            "muted",
        )
        hint.setWordWrap(True)
        self._root.addWidget(hint)

        self.scroll = QScrollArea()
        self.scroll.setWidgetResizable(True)
        self.scroll.setFrameShape(QFrame.Shape.NoFrame)
        self.scroll.setHorizontalScrollBarPolicy(
            Qt.ScrollBarPolicy.ScrollBarAlwaysOff
        )
        self.scroll.setVerticalScrollBarPolicy(
            Qt.ScrollBarPolicy.ScrollBarAsNeeded
            if archives
            else Qt.ScrollBarPolicy.ScrollBarAlwaysOff
        )
        self.list_host = QWidget()
        list_layout = QVBoxLayout(self.list_host)
        list_layout.setContentsMargins(0, 2, 0, 0)
        list_layout.setSpacing(0)
        self.rows: list[widgets.BackupRow] = []
        if archives:
            for index, archive in enumerate(archives):
                row = widgets.BackupRow(
                    archive, last=index == len(archives) - 1
                )
                row.clicked.connect(self._select)
                list_layout.addWidget(row)
                self.rows.append(row)
            list_layout.addStretch(1)
        else:
            list_layout.addStretch(1)
            empty = widgets.label("还没有备份", 14, 600)
            empty.setAlignment(Qt.AlignmentFlag.AlignCenter)
            list_layout.addWidget(empty)
            empty_hint = widgets.label(
                "返回主界面点击“立即备份”创建第一个备份。", 12, 400, "muted"
            )
            empty_hint.setAlignment(Qt.AlignmentFlag.AlignCenter)
            list_layout.addWidget(empty_hint)
            list_layout.addStretch(1)
        self.scroll.setWidget(self.list_host)
        self._root.addWidget(self.scroll, 1)

        row = QHBoxLayout()
        row.setContentsMargins(0, 4, 0, 0)
        row.setSpacing(8)
        row.addStretch(1)
        close = widgets.button("关闭", "secondary", height=36)
        close.setMinimumWidth(88)
        close.setDefault(True)
        close.clicked.connect(self.reject)
        row.addWidget(close)
        self.restore_button = widgets.button(
            "恢复所选", "danger", height=36
        )
        self.restore_button.setMinimumWidth(96)
        self.restore_button.setEnabled(False)
        self.restore_button.clicked.connect(self._ok)
        row.addWidget(self.restore_button)
        self._root.addLayout(row)

    def _select(self, name: str) -> None:
        if self.selected == name:
            return
        self.selected = name
        for row in self.rows:
            row.set_selected(row.name == name)
        self.restore_button.setEnabled(True)

    def _ok(self) -> None:
        if self.selected is None:
            return
        self.value = self.selected
        self.accept()


def _http_url(value: str) -> bool:
    """与 CLI 的 config set 保持一致：只接受 http/https 且有主机名。"""
    value = value.strip()
    if not (value.startswith("http://") or value.startswith("https://")):
        return False
    rest = value.split("://", 1)[1]
    return bool(rest.split("/", 1)[0])


class SettingsDialog(BaseDialog):
    def __init__(
        self,
        parent: QWidget,
        *,
        connection: str,
        local: dict[str, str],
        ssh: dict[str, str],
        backup_dir: str,
        listen: str,
        public_url: str,
        api_origin: str,
        defaults: dict[str, str],
        server_lookup: (
            Callable[[SettingsDialog, str, dict[str, str], int], None] | None
        ) = None,
    ) -> None:
        # 高度按内容算（见 _fit_height）：SSH 比本机多一行，写死会挤重叠。
        super().__init__(parent, "设置", 720)
        self.defaults = defaults
        self._initial_local = dict(local)
        # 打开设置时正在管理的主机：它的服务器配置可以改，另一个主机只能看。
        self._current_connection = (
            connection if connection in ("local", "ssh") else "local"
        )
        self.server_lookup = server_lookup
        self._description(
            "连接方式、路径和上次登录用户名保存在 config.gui.json；SSH 使用系统 "
            "ssh 和你的 ~/.ssh/config（仅密钥/agent）。监听地址、Public URL、"
            "API 地址和备份地址通过 chat-deepseek config set 写入数据目录的 "
            "config.json。"
        )

        self.mode = widgets.SelectField(
            [("local", "本机"), ("ssh", "SSH 远程")]
        )
        self.mode.setAccessibleName("连接方式")
        if connection in ("local", "ssh"):
            self.mode.setValue(connection)
        self.mode.changed.connect(self._mode_changed)

        self.binary = _field("二进制目录")
        self.binary.setText(local.get("binary_dir", ""))
        self.data_dir = _field("数据目录")
        self.data_dir.setText(local.get("data_dir", ""))
        self.host = _field("nas 或 user@nas")
        self.host.setText(ssh.get("host", ""))
        self.remote_binary = _field("/home/you/chat-deepseek")
        self.remote_binary.setText(ssh.get("binary_dir", ""))
        self.remote_data = _field("留空使用远端默认数据目录")
        self.remote_data.setText(ssh.get("data_dir", ""))

        self.backup_dir = _field("留空表示数据目录下的 backups/")
        self.backup_dir.setText(backup_dir)
        self.listen = _field("监听地址")
        self.listen.setText(listen)
        self.public_url = _field("未设置")
        self.public_url.setText(public_url)
        self.api_origin = _field("API 地址")
        self.api_origin.setText(api_origin)
        self.backup_row, self.backup_pick = self._browse_row(
            self.backup_dir, "备份地址"
        )

        # 服务器配置（备份/监听/Public URL/API）属于每台主机自己的
        # config.json；切连接方式时要按目标主机重新读，不能沿用上一台的。
        self._server_fields = {
            "backup_dir": self.backup_dir,
            "listen": self.listen,
            "public_url": self.public_url,
            "api_origin": self.api_origin,
        }
        self._server_placeholders = {
            key: field.edit.placeholderText()
            for key, field in self._server_fields.items()
        }
        self._server_values: dict[str, dict[str, str]] = {
            self.mode.value(): self._read_server_fields()
        }
        self._server_mode = self.mode.value()
        self._server_generation = 0
        self._server_loading = False

        # 本机与远端各占固定几行，切换时只显示对应的一侧。所有行共用同一张
        # 网格：分两个页面各自布局时标签列各算各的宽度，远端输入框会和下面
        # 的备份/监听地址错位。
        self._local_rows: list[QWidget] = []
        self._ssh_rows: list[QWidget] = []
        rows: list[tuple[list[QWidget], str, QWidget]] = [
            (
                self._local_rows,
                "二进制目录",
                self._browse_row(self.binary, "二进制目录")[0],
            ),
            (
                self._local_rows,
                "数据目录",
                self._browse_row(self.data_dir, "数据目录")[0],
            ),
            (self._ssh_rows, "SSH 主机", self.host),
            (self._ssh_rows, "远端二进制目录", self.remote_binary),
            (self._ssh_rows, "远端数据目录", self.remote_data),
            ([], "备份地址", self.backup_row),
            ([], "监听地址", self.listen),
            ([], "Public URL", self.public_url),
            ([], "API 地址", self.api_origin),
        ]

        grid = QGridLayout()
        grid.setContentsMargins(0, 6, 0, 0)
        grid.setHorizontalSpacing(14)
        grid.setVerticalSpacing(12)
        grid.setColumnStretch(1, 1)
        mode_label = widgets.label("连接方式", 12, 500)
        grid.addWidget(mode_label, 0, 0)
        grid.addWidget(self.mode, 0, 1)
        captions = [mode_label]
        for index, (bucket, caption, editor) in enumerate(rows, start=1):
            caption_label = widgets.label(caption, 12, 500)
            grid.addWidget(caption_label, index, 0)
            grid.addWidget(editor, index, 1)
            captions.append(caption_label)
            bucket.extend((caption_label, editor))

        # 标签列统一成最宽的一条：两套行轮流显示，不固定的话切换连接方式
        # 时整列输入框会左右错开。
        width = max(label.sizeHint().width() for label in captions) + 2
        for label in captions:
            label.setFixedWidth(width)

        self._root.addLayout(grid)
        self._root.addSpacing(4)
        self.server_hint = widgets.label("", 12, 400, "muted")
        self.server_hint.setWordWrap(True)
        self.server_hint.hide()
        self._root.addWidget(self.server_hint)
        self._root.addWidget(self._error_label())
        self._root.addStretch(1)

        row = QHBoxLayout()
        row.setSpacing(8)
        restore = widgets.button("恢复默认", "ghost", height=36)
        restore.clicked.connect(self._restore_defaults)
        row.addWidget(restore)
        row.addStretch(1)
        cancel = widgets.button("取消", "secondary", height=36)
        cancel.setMinimumWidth(88)
        cancel.clicked.connect(self.reject)
        row.addWidget(cancel)
        save = widgets.button("保存", "primary", height=36)
        save.setMinimumWidth(92)
        save.setDefault(True)
        save.clicked.connect(self._ok)
        row.addWidget(save)
        self._root.addLayout(row)
        for editor in (
            self.binary,
            self.data_dir,
            self.host,
            self.remote_binary,
            self.remote_data,
        ):
            editor.edit.editingFinished.connect(self._path_edited)
        self._sync_mode()
        self._load_server_values()

    def _browse_row(
        self, editor: widgets.Field, caption: str
    ) -> tuple[QWidget, widgets.ActionButton]:
        holder = QWidget()
        row = QHBoxLayout(holder)
        row.setContentsMargins(0, 0, 0, 0)
        row.setSpacing(8)
        row.addWidget(editor, 1)
        pick = widgets.button("浏览", "secondary", height=38, small=True)
        pick.setMinimumWidth(64)
        pick.clicked.connect(
            lambda _checked=False, editor=editor, caption=caption: self._pick(
                caption, editor
            )
        )
        row.addWidget(pick)
        return holder, pick

    def _mode_changed(self, _value: str) -> None:
        self._stash_server_values()
        self._sync_mode()
        self._load_server_values()

    def _sync_mode(self) -> None:
        remote = self.mode.value() == "ssh"
        for widget in self._local_rows:
            widget.setVisible(not remote)
        for widget in self._ssh_rows:
            widget.setVisible(remote)
        # 远端路径不能用本机文件对话框选。
        self.backup_pick.setVisible(not remote)
        self._error.hide()
        self._fit_height()

    # ---- 每台主机自己的服务器配置 ---------------------------------------

    def _read_server_fields(self) -> dict[str, str]:
        return {
            key: field.text().strip()
            for key, field in self._server_fields.items()
        }

    def _write_server_fields(self, values: dict[str, str]) -> None:
        for key, field in self._server_fields.items():
            field.setText(values.get(key, ""))

    def _stash_server_values(self) -> None:
        """把正在显示的值记到它所属的模式下，切走再切回来时不丢。"""
        if self._server_loading:
            return
        self._server_values[self._server_mode] = self._read_server_fields()

    def _load_server_values(self) -> None:
        mode = self.mode.value()
        self._server_mode = mode
        self._error.hide()
        cached = self._server_values.get(mode)
        if cached is not None:
            self._server_loading = False
            self._write_server_fields(cached)
            self._set_server_editable(mode == self._current_connection)
            self._update_server_hint(mode)
            return
        if self.server_lookup is None:
            # 没有读取能力时宁可留空，也不要把当前主机的值冒充成另一台的。
            self._server_loading = False
            self._write_server_fields({})
            self._set_server_editable(mode == self._current_connection)
            self._update_server_hint(mode)
            return
        self._server_loading = True
        self._server_generation += 1
        self._set_server_editable(False)
        self._write_server_fields({})
        for field in self._server_fields.values():
            field.edit.setPlaceholderText("正在读取该主机的 config.json…")
        self._update_server_hint(mode)
        self._fit_height()
        self.server_lookup(self, mode, self._target_values(mode), self._server_generation)

    def _target_values(self, mode: str) -> dict[str, str]:
        if mode == "local":
            return {
                "binary_dir": self.binary.text().strip(),
                "data_dir": self.data_dir.text().strip(),
            }
        return {
            "host": self.host.text().strip(),
            "binary_dir": self.remote_binary.text().strip(),
            "data_dir": self.remote_data.text().strip(),
        }

    def _set_server_editable(self, editable: bool) -> None:
        for field in self._server_fields.values():
            field.setEnabled(editable)

    def _server_placeholder(self, mode: str) -> str:
        if mode == "ssh":
            return "留空表示远端数据目录下的 backups/"
        return self._server_placeholders.get("backup_dir", "")

    def _update_server_hint(self, mode: str) -> None:
        """告诉用户这几项属于哪台主机、为什么不能改。"""
        if self._server_loading:
            self._hint(f"正在读取{self._host_label(mode)}的 config.json…")
            return
        if mode != self._current_connection:
            self._hint(
                f"以上四项来自{self._host_label(mode)}的 config.json。"
                "点“保存”会切换连接，切换之后才能修改。"
            )
            return
        self._hint("")

    def _host_label(self, mode: str) -> str:
        if mode == "local":
            return "本机"
        host = self.host.text().strip()
        return f" SSH 主机 {host} " if host else " SSH 主机 "

    def _hint(self, text: str) -> None:
        if text:
            self.server_hint.setText(text)
            self.server_hint.show()
        else:
            self.server_hint.hide()
        self._fit_height()

    def _fail_server(self, mode: str, message: str) -> None:
        self._server_loading = False
        self._write_server_fields({})
        self._set_server_editable(False)
        for field in self._server_fields.values():
            field.edit.setPlaceholderText("未读取到该主机的配置")
        self._hint(f"读取{self._host_label(mode)}的 config.json 失败：{message}")
        self._fit_height()

    def apply_server(
        self,
        connection: str,
        generation: int,
        values: dict[str, str] | None,
        error: str = "",
    ) -> None:
        """读取完成（或失败）后回填；只接受当前模式和最新一次请求。"""
        if generation != self._server_generation or connection != self.mode.value():
            return
        if values is None:
            self._fail_server(connection, error or "无法读取 config.json")
            return
        for key, field in self._server_fields.items():
            field.edit.setPlaceholderText(self._server_placeholders[key])
        self._server_loading = False
        self._server_values[connection] = dict(values)
        self._server_mode = connection
        self._write_server_fields(self._server_values[connection])
        self._set_server_editable(connection == self._current_connection)
        self._update_server_hint(connection)

    def _path_edited(self) -> None:
        """主机或路径改了，缓存里的服务器配置就不属于这个目标了。"""
        mode = self.mode.value()
        if mode == self._current_connection or self._server_loading:
            return
        self._server_values.pop(mode, None)
        self._load_server_values()

    def _fit_height(self) -> None:
        """按当前显示的行重新定高。

        两套行高度不同，写死高度会把远端那三行输入框压到互相重叠；这里量
        当前内容再固定，隐藏的行不占位，切回本机也不会留一大片空白。
        """
        self._root.activate()
        needed = max(
            self._root.totalSizeHint().height(),
            self._root.totalMinimumSize().height(),
            520,
        )
        self.setFixedHeight(needed)

    def _pick(self, caption: str, editor: widgets.Field) -> None:
        chosen = _browse(self, f"选择{caption}", editor.text())
        if chosen:
            editor.setText(chosen)

    def _restore_defaults(self) -> None:
        if self.mode.value() != self._current_connection:
            self._hint("这些配置属于另一台主机，切换过去之后才能恢复默认。")
            return
        if self.mode.value() == "ssh":
            self.remote_binary.clear()
            self.remote_data.clear()
            # 远端备份地址留空，让 CLI 用数据目录下的 backups/。
            self.backup_dir.clear()
        else:
            self.binary.setText(self.defaults["binary"])
            self.data_dir.setText(self.defaults["data_dir"])
            self.backup_dir.setText(self.defaults["backup_dir"])
        self.listen.setText(self.defaults["listen"])
        self.public_url.setText(self.defaults["public_url"])
        self.api_origin.setText(self.defaults["api_origin"])
        self._error.hide()

    def _ok(self) -> None:
        if self._server_loading:
            self._show_error("正在读取目标主机的配置，请稍候再保存。")
            return
        mode = self.mode.value()
        local_binary = self.binary.text().strip()
        local_data = self.data_dir.text().strip()
        host = self.host.text().strip()
        remote_binary = self.remote_binary.text().strip()
        remote_data = self.remote_data.text().strip()
        backup_dir = self.backup_dir.text().strip()
        listen = self.listen.text().strip()
        api_origin = self.api_origin.text().strip()

        if mode == "local":
            if not local_binary:
                self._show_error("请选择二进制目录")
                return
            if not cli.binary_in(local_binary).is_file():
                self._show_error(f"目录里没有 {cli.binary_name()}")
                return
            if not local_data:
                self._show_error("数据目录不能为空")
                return
        else:
            if not host:
                self._show_error("请填写 SSH 主机")
                return
            if not remote_binary:
                self._show_error("请填写远端二进制目录")
                return
            if not remote_binary.startswith("/"):
                self._show_error("远端二进制目录必须是绝对路径")
                return
            if remote_data and not remote_data.startswith("/"):
                self._show_error("远端数据目录必须是绝对路径，留空使用远端默认值")
                return

        if not listen:
            self._show_error("监听地址不能为空")
            return
        if backup_dir:
            absolute = (
                posixpath.isabs(backup_dir)
                if mode == "ssh"
                else Path(backup_dir).expanduser().is_absolute()
            )
            if not absolute:
                self._show_error("备份地址必须是绝对路径，留空表示默认目录")
                return
        public_url = self.public_url.text().strip()
        if public_url and not _http_url(public_url):
            self._show_error("Public URL 必须以 http:// 或 https:// 开头")
            return
        if api_origin and not _http_url(api_origin):
            self._show_error("API 地址必须以 http:// 或 https:// 开头")
            return

        if mode == "local":
            old_data = Path(self._initial_local.get("data_dir", "")).expanduser()
            new_data = Path(local_data).expanduser()
            if backup_dir == str(old_data / "backups") and new_data != old_data:
                # 数据目录换了而备份地址还是旧默认值：清空，让它跟随新数据目录。
                backup_dir = ""
        self.value = {
            "connection": mode,
            "local": {"binary_dir": local_binary, "data_dir": local_data},
            "ssh": {
                "host": host,
                "binary_dir": remote_binary,
                "data_dir": remote_data,
            },
            "backup_dir": backup_dir,
            "listen": listen,
            "public_url": public_url,
            "api_origin": api_origin or server_config.DEFAULT_API_ORIGIN,
        }
        self.accept()


class InstallServiceDialog(BaseDialog):
    """安装/更新 systemd 服务。

    用户级直接执行安装；系统级需要 root，GUI 只生成安装命令和 CLI 帮助命令。
    """

    def __init__(
        self,
        parent: QWidget,
        *,
        binary: str,
        data_dir: str,
        linger: bool,
        username: str,
        host: str = "",
        installed: bool = False,
        enabled: bool = False,
        active: bool = False,
    ) -> None:
        title = "更新服务" if installed else "安装服务"
        super().__init__(parent, title, 720)
        self.binary = binary
        self.data_dir = data_dir
        self.username = username or "<user>"
        self.host = host.strip()
        self.scope = "user"
        self.installed = installed
        self.linger = linger
        self.active = active

        grid = QGridLayout()
        grid.setContentsMargins(0, 6, 0, 0)
        grid.setHorizontalSpacing(14)
        grid.setVerticalSpacing(12)
        grid.setColumnStretch(1, 1)
        self._grid = grid
        self._row = 0

        self.scope_field = None
        if not installed:
            self.scope_field = widgets.SelectField(
                [
                    ("user", "用户目录"),
                    ("system", "系统目录"),
                ]
            )
            self.scope_field.setAccessibleName("安装位置")
            self.scope_field.changed.connect(self._set_scope)
            self._add_row("安装位置", self.scope_field)

        self.enable = widgets.Switch()
        self.enable.setChecked(True if not installed else enabled)
        self._add_row("开机自启", self._switch_holder(self.enable))

        if installed:
            start_label = "更新后重启服务" if active else "更新后启动服务"
        else:
            start_label = "安装后立即启动"
        self.start_now = widgets.Switch()
        self.start_now.setChecked(True)
        self.start_label = widgets.label(start_label, 12, 500)
        self.start_holder = self._switch_holder(self.start_now)
        grid.addWidget(self.start_label, self._row, 0)
        grid.addWidget(self.start_holder, self._row, 1)
        self._row += 1

        self.linger_label = widgets.label("linger", 12, 500)
        self.linger_field = widgets.DisplayField(self._linger_command())
        self.linger_button = widgets.button(
            "复制", "secondary", height=38, small=True
        )
        self.linger_button.setMinimumWidth(64)
        self.linger_button.setToolTip(self._linger_command())
        self.linger_button.clicked.connect(
            lambda _checked=False: self._copy(
                self._linger_command(), self.linger_button
            )
        )
        self.linger_holder = self._copy_holder(
            self.linger_field, self.linger_button
        )
        grid.addWidget(self.linger_label, self._row, 0)
        grid.addWidget(self.linger_holder, self._row, 1)
        self._row += 1

        self.install_command_text = self._install_command()
        self.install_command_field = widgets.DisplayField(
            self.install_command_text
        )
        self.install_command_button = self._command_copy_button(
            "安装", lambda _checked=False: self._copy(
                self.install_command_text, self.install_command_button
            )
        )
        self.install_command_label = widgets.label("安装命令", 12, 500)
        self.install_command_holder = self._copy_holder(
            self.install_command_field, self.install_command_button
        )
        grid.addWidget(self.install_command_label, self._row, 0)
        grid.addWidget(self.install_command_holder, self._row, 1)
        self._row += 1

        self.help_command_text = service_plan.service_help_command(
            self.binary, host=self.host
        )
        self.help_command_field = widgets.DisplayField(self.help_command_text)
        self.help_command_button = self._command_copy_button(
            "帮助", lambda _checked=False: self._copy(
                self.help_command_text, self.help_command_button
            )
        )
        self.help_command_label = widgets.label("CLI 帮助", 12, 500)
        self.help_command_holder = self._copy_holder(
            self.help_command_field, self.help_command_button
        )
        grid.addWidget(self.help_command_label, self._row, 0)
        grid.addWidget(self.help_command_holder, self._row, 1)
        self._row += 1

        self.enable.toggled.connect(self._update_install_command)

        self._root.addLayout(grid)
        self._root.addWidget(self._error_label())
        self._root.addStretch(1)
        self.primary_button = self._buttons(self._primary_label(), self._ok)
        self._sync_scope()

    def _add_row(self, caption: str, editor: QWidget) -> QWidget:
        self._grid.addWidget(widgets.label(caption, 12, 500), self._row, 0)
        self._grid.addWidget(editor, self._row, 1)
        self._row += 1
        return editor

    def _copy_holder(
        self, field: widgets.DisplayField, button: widgets.ActionButton
    ) -> QWidget:
        holder = QWidget()
        row = QHBoxLayout(holder)
        row.setContentsMargins(0, 0, 0, 0)
        row.setSpacing(8)
        row.addWidget(field, 1)
        row.addWidget(button, 0, Qt.AlignmentFlag.AlignVCenter)
        return holder

    def _command_copy_button(self, name: str, handler) -> widgets.ActionButton:
        button = widgets.button("复制", "secondary", height=38, small=True)
        button.setMinimumWidth(64)
        button.setAccessibleName(f"复制{name}命令")
        button.clicked.connect(handler)
        return button

    def _linger_command(self) -> str:
        return service_plan.linger_command(
            self.username, enable=not self.linger, host=self.host
        )

    def _primary_label(self) -> str:
        return "更新" if self.installed else "安装"

    def _enable_value(self) -> bool:
        return self.enable.isChecked()

    def _install_command(self) -> str:
        return service_plan.system_install_command(
            self.binary,
            self.data_dir,
            run_as=self.username,
            enable=self._enable_value(),
            host=self.host,
        )

    def _update_install_command(self, *_args) -> None:
        self.install_command_text = self._install_command()
        self.install_command_field.setText(self.install_command_text)
        self.install_command_button.setToolTip(self.install_command_text)
        self.install_command_button.setText("复制")

    def _sync_scope(self) -> None:
        system = self.scope == "system"
        self._sync_linger()
        self._sync_commands()
        self.start_label.setVisible(not system)
        self.start_holder.setVisible(not system)
        self.primary_button.setVisible(not system)
        self.cancel_button.setText("关闭" if system else "取消")
        self.cancel_button.setDefault(system)
        self.primary_button.setDefault(not system)
        self.primary_button.setText(self._primary_label())

    def _sync_linger(self) -> None:
        visible = self.scope == "user"
        self.linger_label.setVisible(visible)
        self.linger_holder.setVisible(visible)
        if visible:
            self.linger_field.setText(self._linger_command())
            self.linger_button.setToolTip(self._linger_command())
            self._sync_linger_button()

    def _sync_linger_button(self) -> None:
        self.linger_button.setVisible(self.scope == "user")

    def _sync_commands(self) -> None:
        visible = self.scope == "system"
        for widget in (
            self.install_command_label,
            self.install_command_holder,
            self.help_command_label,
            self.help_command_holder,
        ):
            widget.setVisible(visible)
        if visible:
            self.help_command_button.setText("复制")
            self._update_install_command()

    def _set_scope(self, scope: str) -> None:
        if scope not in ("user", "system") or scope == self.scope:
            return
        self.scope = scope
        self._sync_scope()
        self.adjustSize()

    def _switch_holder(self, switch: widgets.Switch) -> QWidget:
        holder = QWidget()
        holder.setFixedHeight(40)
        row = QHBoxLayout(holder)
        row.setContentsMargins(0, 0, 0, 0)
        row.addStretch(1)
        row.addWidget(switch, 0, Qt.AlignmentFlag.AlignVCenter)
        return holder

    def _copy(self, text: str, button: widgets.ActionButton) -> None:
        QGuiApplication.clipboard().setText(text)
        button.setText("已复制")

    def _ok(self) -> None:
        self.value = {
            "scope": "user",
            "enable": self._enable_value(),
            "start": self.start_now.isChecked(),
            "force": self.installed,
        }
        self.accept()


def ask_confirm(
    parent: QWidget, title: str, message: str, danger: bool = False
) -> bool:
    dialog = ConfirmDialog(parent, title, message, danger=danger)
    dialog.exec_modal()
    return dialog.value is True


def show_message(
    parent: QWidget, title: str, message: str, details: str | None = None
) -> None:
    dialog = MessageDialog(parent, title, message, details)
    dialog.exec_modal()
