"""Qt 对话框：账户、密码、组、确认、消息与设置。"""

from __future__ import annotations

from pathlib import Path

from PySide6.QtCore import Qt
from PySide6.QtGui import QFontDatabase
from PySide6.QtWidgets import (
    QButtonGroup,
    QDialog,
    QFileDialog,
    QGridLayout,
    QHBoxLayout,
    QLabel,
    QVBoxLayout,
    QWidget,
)

import cli
import server_config
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
        primary_button = widgets.button(
            primary, "danger" if danger else "primary", height=36
        )
        primary_button.setMinimumWidth(92)
        primary_button.setDefault(True)
        primary_button.clicked.connect(on_primary)
        row.addWidget(primary_button)
        self._root.addLayout(row)

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


class AccountDialog(BaseDialog):
    def __init__(self, parent: QWidget, mode: str) -> None:
        title = "创建 Owner" if mode == "owner" else "添加用户"
        super().__init__(parent, title, 480, 350 if mode == "owner" else 420)
        self.mode = mode
        description = (
            "第一个账户会成为 Owner。"
            if mode == "owner"
            else "新用户默认属于 User 组，可附加 Admin 或 Owner。"
        )
        self._description(description)
        self.username = _field("用户名")
        self.password = _field("密码", password=True)
        self.confirm = _field("再次输入密码", password=True)
        for entry in (self.username, self.password, self.confirm):
            self._root.addWidget(entry)
        self.admin = widgets.CheckBox("Admin")
        self.owner = widgets.CheckBox("Owner")
        if mode == "user":
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
            if self.admin.isChecked():
                groups.append("Admin")
            if self.owner.isChecked():
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


class SettingsDialog(BaseDialog):
    def __init__(
        self,
        parent: QWidget,
        binary: str,
        data_dir: str,
        listen: str,
        public_url: str,
        api_origin: str,
        defaults: dict[str, str],
    ) -> None:
        super().__init__(parent, "设置", 720, 430)
        self.defaults = defaults
        self._description("这些设置只作用于当前窗口，不会写入配置文件。")

        grid = QGridLayout()
        grid.setContentsMargins(0, 6, 0, 0)
        grid.setHorizontalSpacing(14)
        grid.setVerticalSpacing(12)
        grid.setColumnStretch(1, 1)

        self.binary = _field("二进制目录")
        self.binary.setText(binary)
        self.data_dir = _field("数据目录")
        self.data_dir.setText(data_dir)
        self.listen = _field("监听地址")
        self.listen.setText(listen)
        self.public_url = _field("未设置")
        self.public_url.setText(public_url)
        self.api_origin = _field("API 地址")
        self.api_origin.setText(api_origin)

        rows = (
            ("二进制目录", self.binary, True),
            ("数据目录", self.data_dir, True),
            ("监听地址", self.listen, False),
            ("Public URL", self.public_url, False),
            ("API 地址", self.api_origin, False),
        )
        for index, (caption, editor, browse) in enumerate(rows):
            grid.addWidget(widgets.label(caption, 12, 500), index, 0)
            if browse:
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
                grid.addWidget(holder, index, 1)
            else:
                grid.addWidget(editor, index, 1)

        self._root.addLayout(grid)
        self._root.addSpacing(4)
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

    def _pick(self, caption: str, editor: widgets.Field) -> None:
        chosen = _browse(self, f"选择{caption}", editor.text())
        if chosen:
            editor.setText(chosen)

    def _restore_defaults(self) -> None:
        self.binary.setText(self.defaults["binary"])
        self.data_dir.setText(self.defaults["data_dir"])
        self.listen.setText(self.defaults["listen"])
        self.public_url.setText(self.defaults["public_url"])
        self.api_origin.setText(self.defaults["api_origin"])
        self._error.hide()

    def _ok(self) -> None:
        binary_dir = self.binary.text().strip()
        data_dir = self.data_dir.text().strip()
        listen = self.listen.text().strip()
        api_origin = self.api_origin.text().strip()
        if not binary_dir:
            self._show_error("请选择二进制目录")
            return
        if not cli.binary_in(binary_dir).is_file():
            self._show_error(f"目录里没有 {cli.binary_name()}")
            return
        if not data_dir:
            self._show_error("数据目录不能为空")
            return
        if not listen:
            self._show_error("监听地址不能为空")
            return
        self.value = {
            "binary": binary_dir,
            "data_dir": data_dir,
            "listen": listen,
            "public_url": self.public_url.text().strip(),
            "api_origin": api_origin or server_config.DEFAULT_API_ORIGIN,
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
