from __future__ import annotations

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import service_plan


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

    def test_disable_is_optional(self) -> None:
        self.assertEqual(
            service_plan.install_args(
                "/srv/data", enable=False, disable=True
            ),
            ["service", "install", "--disable", "--data-dir", "/srv/data"],
        )
        with self.assertRaises(ValueError):
            service_plan.install_args(
                "/srv/data", enable=True, disable=True
            )

    def test_system_install_args(self) -> None:
        args = service_plan.install_args(
            "/srv/chat data",
            enable=True,
            force=True,
            scope="system",
            run_as="gx",
        )
        self.assertEqual(
            args,
            [
                "service",
                "install",
                "--system",
                "--run-as",
                "gx",
                "--enable",
                "--force",
                "--data-dir",
                "/srv/chat data",
            ],
        )

    def test_user_install_args_have_no_run_as(self) -> None:
        args = service_plan.install_args(
            "/srv/data", enable=False, scope="user", run_as="gx"
        )
        self.assertNotIn("--system", args)
        self.assertNotIn("--run-as", args)

    def test_linger_command(self) -> None:
        self.assertEqual(
            service_plan.linger_command("gx"), "loginctl enable-linger gx"
        )
        self.assertEqual(
            service_plan.linger_command("a b"), "loginctl enable-linger 'a b'"
        )
        self.assertEqual(
            service_plan.linger_command("gx", enable=False),
            "loginctl disable-linger gx",
        )
        self.assertEqual(
            service_plan.linger_command("a b", enable=False),
            "loginctl disable-linger 'a b'",
        )


class UpdateArgsTest(unittest.TestCase):
    def test_update_always_sets_enable_or_disable(self) -> None:
        self.assertEqual(
            service_plan.update_args("/srv/data", enable=True),
            [
                "service",
                "install",
                "--enable",
                "--force",
                "--data-dir",
                "/srv/data",
            ],
        )
        self.assertEqual(
            service_plan.update_args("/srv/data", enable=False),
            [
                "service",
                "install",
                "--disable",
                "--force",
                "--data-dir",
                "/srv/data",
            ],
        )


class SystemCommandsTest(unittest.TestCase):
    def test_install_command(self) -> None:
        command = service_plan.system_install_command(
            "/opt/chat data/chat-deepseek",
            "/srv/chat data",
            run_as="gx",
            enable=True,
        )
        self.assertEqual(
            command,
            "sudo '/opt/chat data/chat-deepseek' service install "
            "--system --run-as gx --enable --data-dir '/srv/chat data'",
        )

    def test_install_command_without_enable(self) -> None:
        command = service_plan.system_install_command(
            "/opt/chat-deepseek",
            "/srv/data",
            run_as="a b",
            enable=False,
        )
        self.assertEqual(
            command,
            "sudo /opt/chat-deepseek service install --system "
            "--run-as 'a b' --data-dir /srv/data",
        )

    def test_help_command(self) -> None:
        self.assertEqual(
            service_plan.service_help_command("/opt/chat-deepseek"),
            "/opt/chat-deepseek service --help",
        )
        self.assertEqual(
            service_plan.service_help_command("/opt/chat data/chat-deepseek"),
            "'/opt/chat data/chat-deepseek' service --help",
        )

if __name__ == "__main__":
    unittest.main()
