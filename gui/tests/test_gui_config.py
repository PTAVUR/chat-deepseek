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
            values = gui_config.load(Path(temp, "config.gui.json"))
        self.assertEqual(values, {})

    def test_invalid_json_returns_empty(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp, "config.gui.json")
            path.write_text("{", encoding="utf-8")
            values = gui_config.load(path)
        self.assertEqual(values, {})

    def test_reads_only_known_string_values(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp, "config.gui.json")
            path.write_text(
                json.dumps(
                    {
                        "binary": " /opt/chat-deepseek ",
                        "data_dir": "/srv/data",
                        "backup_dir": 3,
                        "unknown": "ignored",
                    }
                ),
                encoding="utf-8",
            )
            values = gui_config.load(path)
        self.assertEqual(
            values,
            {"binary": "/opt/chat-deepseek", "data_dir": "/srv/data"},
        )


class SaveTest(unittest.TestCase):
    def test_round_trip_and_permissions(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp, "nested", "config.gui.json")
            gui_config.save(
                path,
                {
                    "binary": "/opt/chat-deepseek",
                    "data_dir": "/srv/chat data",
                    "backup_dir": "/srv/backups",
                },
            )
            values = gui_config.load(path)
            mode = os.stat(path).st_mode & 0o777
        self.assertEqual(values["data_dir"], "/srv/chat data")
        self.assertEqual(values["backup_dir"], "/srv/backups")
        if os.name == "posix":
            self.assertEqual(mode, 0o600)



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
