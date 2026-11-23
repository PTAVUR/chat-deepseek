from __future__ import annotations

import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import server_config


class DefaultsTest(unittest.TestCase):
    def test_defaults_follow_binary_dir(self) -> None:
        values = server_config.defaults("/opt/chat-deepseek")
        self.assertEqual(values["binary"], "/opt/chat-deepseek")
        self.assertEqual(values["data_dir"], "/opt/chat-deepseek/data")
        self.assertEqual(
            values["backup_dir"], "/opt/chat-deepseek/data/backups"
        )
        self.assertEqual(values["listen"], server_config.DEFAULT_LISTEN)
        self.assertEqual(values["api_origin"], server_config.DEFAULT_API_ORIGIN)
        self.assertEqual(values["public_url"], "")


class MacDefaultsTest(unittest.TestCase):
    def test_data_lives_in_application_support(self) -> None:
        with (
            mock.patch.object(server_config.sys, "platform", "darwin"),
            mock.patch.dict(os.environ, {"HOME": "/tmp/home"}),
        ):
            values = server_config.defaults("/Applications/chat-deepseek")
        self.assertEqual(
            values["data_dir"],
            "/tmp/home/Library/Application Support/chat-deepseek/data",
        )
        self.assertEqual(
            values["backup_dir"],
            "/tmp/home/Library/Application Support/chat-deepseek/data/backups",
        )


class SaveTest(unittest.TestCase):
    def test_writes_values_and_preserves_unknown_fields(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            data = Path(temp, "data")
            data.mkdir()
            Path(data, "config.json").write_text(
                json.dumps({"listen": "old", "future": {"keep": True}}),
                encoding="utf-8",
            )
            server_config.save(
                data,
                {
                    "listen": "0.0.0.0:9000",
                    "public_url": "https://chat.example.com",
                    "api_origin": "https://api.example.com",
                },
            )
            raw = json.loads(Path(data, "config.json").read_text(encoding="utf-8"))
            values = server_config.load(data)
            mode = os.stat(Path(data, "config.json")).st_mode & 0o777
        self.assertEqual(raw["future"], {"keep": True})
        self.assertEqual(values["listen"], "0.0.0.0:9000")
        self.assertEqual(values["public_url"], "https://chat.example.com")
        self.assertEqual(values["api_origin"], "https://api.example.com")
        if os.name == "posix":
            self.assertEqual(mode, 0o600)

    def test_creates_data_directory(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            data = Path(temp, "nested", "data")
            server_config.save(data, {"listen": "127.0.0.1:9000"})
            self.assertTrue(Path(data, "config.json").is_file())


class LoadTest(unittest.TestCase):
    def test_missing_file_uses_defaults(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            values = server_config.load(temp)
        self.assertEqual(values["listen"], server_config.DEFAULT_LISTEN)
        self.assertEqual(values["api_origin"], server_config.DEFAULT_API_ORIGIN)
        self.assertEqual(values["public_url"], "")

    def test_reads_existing_config(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            Path(temp, "config.json").write_text(
                json.dumps(
                    {
                        "listen": "0.0.0.0:9000",
                        "public_url": "https://chat.example.com",
                        "api_origin": "https://api.example.com",
                    }
                ),
                encoding="utf-8",
            )
            values = server_config.load(temp)
        self.assertEqual(values["listen"], "0.0.0.0:9000")
        self.assertEqual(values["public_url"], "https://chat.example.com")
        self.assertEqual(values["api_origin"], "https://api.example.com")

    def test_blank_values_fall_back(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            Path(temp, "config.json").write_text(
                json.dumps({"listen": " ", "api_origin": ""}), encoding="utf-8"
            )
            values = server_config.load(temp)
        self.assertEqual(values["listen"], server_config.DEFAULT_LISTEN)
        self.assertEqual(values["api_origin"], server_config.DEFAULT_API_ORIGIN)

    def test_invalid_json_falls_back(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            Path(temp, "config.json").write_text("{", encoding="utf-8")
            values = server_config.load(temp)
        self.assertEqual(values["listen"], server_config.DEFAULT_LISTEN)


if __name__ == "__main__":
    unittest.main()
