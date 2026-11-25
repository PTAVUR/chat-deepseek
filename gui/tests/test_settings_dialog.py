"""设置对话框布局：SSH 比本机多一行，输入框不能被压到重叠或左右错位。"""

from __future__ import annotations

import os
import sys
import unittest
from pathlib import Path

# 无头环境下也要能建窗口；显式指定平台，免得依赖运行机器的显示服务。
os.environ.setdefault("QT_QPA_PLATFORM", "offscreen")

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from PySide6.QtCore import QPoint
from PySide6.QtWidgets import QApplication, QWidget

import dialogs

DEFAULTS = {
    "binary": "",
    "data_dir": "",
    "backup_dir": "",
    "listen": "127.0.0.1:8090",
    "public_url": "",
    "api_origin": "",
}

FIELDS = (
    ("binary", "local"),
    ("data_dir", "local"),
    ("host", "ssh"),
    ("remote_binary", "ssh"),
    ("remote_data", "ssh"),
    ("backup_dir", None),
    ("listen", None),
    ("public_url", None),
    ("api_origin", None),
)


class SettingsDialogLayoutTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.app = QApplication.instance() or QApplication([])

    def setUp(self) -> None:
        self._dialogs: list[dialogs.SettingsDialog] = []

    def tearDown(self) -> None:
        for dialog in self._dialogs:
            dialog.close()
            dialog.deleteLater()
        self.app.processEvents()

    def _dialog(
        self, mode: str, server_lookup=None
    ) -> dialogs.SettingsDialog:
        dialog = dialogs.SettingsDialog(
            None,
            connection=mode,
            local={"binary_dir": "/opt/bin", "data_dir": "/opt/data"},
            ssh={"host": "nas", "binary_dir": "/srv/bin", "data_dir": ""},
            backup_dir="/opt/data/backups",
            listen="127.0.0.1:8090",
            public_url="",
            api_origin="https://api.deepseek.com",
            defaults=DEFAULTS,
            server_lookup=server_lookup,
        )
        dialog.show()
        self.app.processEvents()
        self._dialogs.append(dialog)
        return dialog

    def _visible(self, dialog: QWidget) -> list[tuple[str, int, int, int]]:
        """返回当前可见输入框的 (名字, x, y, 高)，坐标换算到对话框。"""
        rows = []
        for name, _mode in FIELDS:
            field = getattr(dialog, name)
            if field.isVisible():
                point = field.mapTo(dialog, QPoint(0, 0))
                rows.append((name, point.x(), point.y(), field.height()))
        return rows

    def _assert_layout(self, dialog: QWidget, mode: str) -> None:
        rows = self._visible(dialog)
        self.assertTrue(rows, f"{mode}: 没有任何输入框可见")
        for upper, lower in zip(rows, rows[1:]):
            self.assertGreaterEqual(
                lower[2],
                upper[2] + upper[3],
                f"{mode}: {upper[0]} 和 {lower[0]} 重叠",
            )
        self.assertEqual(
            len({row[1] for row in rows}), 1, f"{mode}: 输入框没有对齐同一列"
        )
        for name, _x, y, height in rows:
            self.assertLessEqual(
                y + height, dialog.height(), f"{mode}: {name} 超出弹窗下沿"
            )

    def test_ssh_rows_fit(self) -> None:
        dialog = self._dialog("ssh")
        self._assert_layout(dialog, "ssh")
        self.assertEqual(len(self._visible(dialog)), 7)
        # 远端路径不能用本机文件对话框选。
        self.assertFalse(dialog.backup_pick.isVisible())

    def test_local_rows_fit(self) -> None:
        dialog = self._dialog("local")
        self._assert_layout(dialog, "local")
        self.assertEqual(len(self._visible(dialog)), 6)
        self.assertTrue(dialog.backup_pick.isVisible())

    def test_switch_keeps_alignment(self) -> None:
        dialog = self._dialog("local")
        local_x = dialog.backup_dir.mapTo(dialog, QPoint(0, 0)).x()
        local_height = dialog.height()
        dialog.mode.setValue("ssh")
        self.app.processEvents()
        self._assert_layout(dialog, "ssh")
        # SSH 多一行，弹窗要跟着长高，但输入框仍在同一列。
        self.assertGreater(dialog.height(), local_height)
        self.assertEqual(
            dialog.backup_dir.mapTo(dialog, QPoint(0, 0)).x(), local_x
        )
        self.assertFalse(dialog.backup_pick.isVisible())
        dialog.mode.setValue("local")
        self.app.processEvents()
        self._assert_layout(dialog, "local")
        self.assertEqual(dialog.height(), local_height)


class ServerConfigPerHostTest(unittest.TestCase):
    """备份地址等四项属于每台主机自己的 config.json。"""

    @classmethod
    def setUpClass(cls) -> None:
        cls.app = QApplication.instance() or QApplication([])

    def setUp(self) -> None:
        self._dialogs: list[dialogs.SettingsDialog] = []

    def tearDown(self) -> None:
        for dialog in self._dialogs:
            dialog.close()
            dialog.deleteLater()
        self.app.processEvents()

    def _dialog(self, mode: str, lookup) -> dialogs.SettingsDialog:
        dialog = dialogs.SettingsDialog(
            None,
            connection=mode,
            local={"binary_dir": "/opt/bin", "data_dir": "/opt/data"},
            ssh={"host": "nas", "binary_dir": "/srv/bin", "data_dir": "/srv/data"},
            backup_dir="/opt/data/backups",
            listen="127.0.0.1:8090",
            public_url="",
            api_origin="https://api.deepseek.com",
            defaults=DEFAULTS,
            server_lookup=lookup,
        )
        dialog.show()
        self.app.processEvents()
        self._dialogs.append(dialog)
        return dialog

    def test_switching_connection_reloads_target_config(self) -> None:
        seen: list[str] = []

        def lookup(dialog, connection, target, generation):
            seen.append(connection)
            dialog.apply_server(
                connection,
                generation,
                {
                    "backup_dir": f"/{connection}/backups",
                    "listen": "0.0.0.0:9090",
                    "public_url": f"https://{connection}.example",
                    "api_origin": "https://api.example",
                },
            )

        dialog = self._dialog("local", lookup)
        self.assertEqual(dialog.backup_dir.text(), "/opt/data/backups")
        self.assertTrue(dialog.backup_dir.isEnabled())

        dialog.mode.setValue("ssh")
        self.assertEqual(seen, ["ssh"])
        self.assertEqual(dialog.backup_dir.text(), "/ssh/backups")
        self.assertEqual(dialog.listen.text(), "0.0.0.0:9090")
        # 另一台主机的配置只展示：保存时切目标本来就不会写这四项。
        self.assertFalse(dialog.backup_dir.isEnabled())
        self.assertIn("nas", dialog.server_hint.text())
        self.assertIn("保存", dialog.server_hint.text())

        dialog.mode.setValue("local")
        self.assertEqual(dialog.backup_dir.text(), "/opt/data/backups")
        self.assertTrue(dialog.backup_dir.isEnabled())

    def test_lookup_failure_keeps_fields_empty(self) -> None:
        def lookup(dialog, connection, target, generation):
            dialog.apply_server(connection, generation, None, "连不上")

        dialog = self._dialog("local", lookup)
        dialog.mode.setValue("ssh")
        self.assertEqual(dialog.backup_dir.text(), "")
        self.assertFalse(dialog.backup_dir.isEnabled())
        self.assertIn("连不上", dialog.server_hint.text())


if __name__ == "__main__":
    unittest.main()
