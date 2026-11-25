from __future__ import annotations

import os
import sys
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

    def test_default_backup_dir(self) -> None:
        self.assertEqual(
            server_config.default_backup_dir("/srv/data"),
            "/srv/data/backups",
        )


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


if __name__ == "__main__":
    unittest.main()
