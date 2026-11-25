from __future__ import annotations

import os
import sys
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import service_plan


class UnitPathTest(unittest.TestCase):
    def test_uses_xdg_config_home(self) -> None:
        with mock.patch.dict(
            os.environ, {"XDG_CONFIG_HOME": "/tmp/config"}, clear=False
        ):
            expected = str(
                Path("/tmp/config") / "systemd" / "user" / "chat-deepseek.service"
            )
            self.assertEqual(service_plan.unit_path(), expected)

    def test_falls_back_to_home_config(self) -> None:
        home = Path("/tmp/home")
        with (
            mock.patch.dict(os.environ, {}, clear=False),
            mock.patch.object(service_plan.Path, "home", return_value=home),
        ):
            os.environ.pop("XDG_CONFIG_HOME", None)
            expected = str(
                home / ".config" / "systemd" / "user" / "chat-deepseek.service"
            )
            self.assertEqual(service_plan.unit_path(), expected)


class InstallArgsTest(unittest.TestCase):
    def test_enable_is_optional(self) -> None:
        self.assertEqual(
            service_plan.install_args("/srv/data", enable=True),
            ["service", "install", "--enable", "--data-dir", "/srv/data"],
        )
        self.assertEqual(
            service_plan.install_args("/srv/data", enable=False),
            ["service", "install", "--data-dir", "/srv/data"],
        )
        self.assertEqual(
            service_plan.install_args("/srv/data", enable=True, force=True),
            [
                "service",
                "install",
                "--enable",
                "--force",
                "--data-dir",
                "/srv/data",
            ],
        )

    def test_system_command_quotes_values(self) -> None:
        command = service_plan.system_install_command(
            "/opt/chat deepseek/chat-deepseek",
            "/srv/chat data",
            "gx",
            enable=True,
        )
        self.assertIn("service install --system", command)
        self.assertIn("--run-as gx", command)
        self.assertIn("--enable", command)
        self.assertIn("'/srv/chat data'", command)
        self.assertIn("'/opt/chat deepseek/chat-deepseek'", command)

    def test_system_command_omits_enable(self) -> None:
        command = service_plan.system_install_command(
            "/opt/chat-deepseek", "/srv/data", "gx", enable=False
        )
        self.assertNotIn("--enable", command)

    def test_linger_command(self) -> None:
        self.assertEqual(
            service_plan.linger_command("gx"), "loginctl enable-linger gx"
        )
        self.assertEqual(
            service_plan.linger_command("a b"), "loginctl enable-linger 'a b'"
        )


if __name__ == "__main__":
    unittest.main()
