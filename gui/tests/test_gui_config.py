from __future__ import annotations

import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import gui_config


class LoadTest(unittest.TestCase):
    def test_missing_file_returns_empty(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            config = gui_config.load(Path(temp, "config.gui.json"))
        self.assertTrue(config.empty)
        self.assertEqual(config.connection, "local")

    def test_invalid_json_returns_empty(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp, "config.gui.json")
            path.write_text("{", encoding="utf-8")
            config = gui_config.load(path)
        self.assertTrue(config.empty)

    def test_reads_v2_nested_values(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp, "config.gui.json")
            path.write_text(
                json.dumps(
                    {
                        "version": 2,
                        "connection": "local",
                        "local": {
                            "binary_dir": " /opt/chat-deepseek ",
                            "data_dir": "/srv/data",
                            "username": " owner ",
                            "unknown": "ignored",
                        },
                        "future": {"keep": True},
                    }
                ),
                encoding="utf-8",
            )
            config = gui_config.load(path)
        self.assertEqual(config.version, 2)
        self.assertEqual(config.connection, "local")
        self.assertEqual(config.binary_dir, "/opt/chat-deepseek")
        self.assertEqual(config.data_dir, "/srv/data")
        self.assertEqual(config.username, "owner")
        self.assertFalse(config.migrated)

    def test_migrates_v1_and_returns_backup_dir(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp, "config.gui.json")
            path.write_text(
                json.dumps(
                    {
                        "binary": "/opt/chat-deepseek",
                        "data_dir": "/srv/data",
                        "backup_dir": "/srv/backups",
                        "username": "owner",
                    }
                ),
                encoding="utf-8",
            )
            config = gui_config.load(path)
        self.assertEqual(config.binary_dir, "/opt/chat-deepseek")
        self.assertEqual(config.data_dir, "/srv/data")
        self.assertEqual(config.username, "owner")
        self.assertEqual(config.legacy_backup_dir, "/srv/backups")
        self.assertTrue(config.migrated)

    def test_ignores_non_string_values(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp, "config.gui.json")
            path.write_text(
                json.dumps(
                    {
                        "version": 2,
                        "connection": 3,
                        "local": {"binary_dir": 4, "data_dir": " ", "username": None},
                    }
                ),
                encoding="utf-8",
            )
            config = gui_config.load(path)
        self.assertEqual(config.connection, "local")
        self.assertEqual(config.binary_dir, "")
        self.assertEqual(config.data_dir, "")
        self.assertEqual(config.username, "")


class SaveTest(unittest.TestCase):
    def test_round_trip_and_permissions(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp, "nested", "config.gui.json")
            gui_config.save(
                path,
                gui_config.Config(
                    binary_dir="/opt/chat-deepseek",
                    data_dir="/srv/chat data",
                    username="owner",
                ),
            )
            config = gui_config.load(path)
            raw = json.loads(path.read_text(encoding="utf-8"))
            mode = os.stat(path).st_mode & 0o777
        self.assertEqual(config.binary_dir, "/opt/chat-deepseek")
        self.assertEqual(config.data_dir, "/srv/chat data")
        self.assertEqual(config.username, "owner")
        self.assertEqual(raw["version"], 2)
        self.assertEqual(raw["connection"], "local")
        self.assertNotIn("migrated", raw)
        self.assertNotIn("legacy_backup_dir", raw)
        if os.name == "posix":
            self.assertEqual(mode, 0o600)

    def test_save_preserves_connection(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp, "config.gui.json")
            gui_config.save(
                path,
                gui_config.Config(
                    connection="ssh",
                    binary_dir="/opt/chat-deepseek",
                    data_dir="/srv/data",
                ),
            )
            raw = json.loads(path.read_text(encoding="utf-8"))
        self.assertEqual(raw["connection"], "ssh")

    def test_save_preserves_future_sections_and_drops_v1_keys(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp, "config.gui.json")
            path.write_text(
                json.dumps(
                    {
                        "version": 1,
                        "binary": "/old/bin",
                        "data_dir": "/old/data",
                        "backup_dir": "/old/backups",
                        "username": "old",
                        "ssh": {"host": "nas"},
                        "custom": {"keep": True},
                    }
                ),
                encoding="utf-8",
            )
            gui_config.save(
                path,
                gui_config.Config(
                    binary_dir="/new/bin",
                    data_dir="/new/data",
                    username="new",
                ),
            )
            raw = json.loads(path.read_text(encoding="utf-8"))
        self.assertEqual(raw["version"], 2)
        self.assertEqual(raw["local"]["binary_dir"], "/new/bin")
        self.assertEqual(raw["local"]["data_dir"], "/new/data")
        self.assertEqual(raw["local"]["username"], "new")
        self.assertEqual(raw["ssh"], {"host": "nas"})
        self.assertEqual(raw["custom"], {"keep": True})
        for key in ("binary", "data_dir", "backup_dir", "username"):
            self.assertNotIn(key, raw)


class PathTest(unittest.TestCase):
    def test_linux_uses_xdg_config_home(self) -> None:
        with (
            mock.patch.object(gui_config.sys, "platform", "linux"),
            mock.patch.dict(os.environ, {"XDG_CONFIG_HOME": "/tmp/xdg"}),
        ):
            path = gui_config.default_path()
        self.assertEqual(
            path, Path("/tmp/xdg/chat-deepseek/config.gui.json")
        )

    def test_windows_uses_appdata(self) -> None:
        with (
            mock.patch.object(gui_config.sys, "platform", "win32"),
            mock.patch.dict(os.environ, {"APPDATA": "/tmp/appdata"}),
        ):
            path = gui_config.default_path()
        self.assertEqual(
            path, Path("/tmp/appdata/chat-deepseek/config.gui.json")
        )

    def test_macos_uses_application_support(self) -> None:
        with (
            mock.patch.object(gui_config.sys, "platform", "darwin"),
            mock.patch.dict(os.environ, {"HOME": "/tmp/home"}),
        ):
            path = gui_config.default_path()
        self.assertEqual(
            path,
            Path("/tmp/home/Library/Application Support/chat-deepseek/config.gui.json"),
        )


class SeedPathTest(unittest.TestCase):
    def test_macos_app_resources_and_dmg_root(self) -> None:
        executable = "/Applications/chat-deepseek-gui.app/Contents/MacOS/chat-deepseek-gui"
        with (
            mock.patch.object(gui_config.sys, "frozen", True, create=True),
            mock.patch.object(gui_config.sys, "platform", "darwin"),
            mock.patch.object(gui_config.sys, "executable", executable),
        ):
            paths = gui_config.seed_paths()
        self.assertEqual(
            paths,
            [
                Path("/Applications/chat-deepseek-gui.app/Contents/Resources/config.gui.json"),
                Path("/Applications/config.gui.json"),
            ],
        )



if __name__ == "__main__":
    unittest.main()
