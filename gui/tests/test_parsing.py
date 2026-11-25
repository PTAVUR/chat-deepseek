from __future__ import annotations

import sys
import unittest
from datetime import datetime
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import parsing


class ParseUsersTest(unittest.TestCase):
    def test_table(self) -> None:
        text = """USERNAME  GROUPS            DISABLED  ID
alice     User,Admin,Owner  false     4d0f
owner     User,Owner        true      9a1c
"""
        users = parsing.parse_users(text)
        self.assertEqual([user.username for user in users], ["alice", "owner"])
        self.assertEqual(users[0].groups, ("User", "Admin", "Owner"))
        self.assertEqual(users[0].groups_text, "User, Admin, Owner")
        self.assertFalse(users[0].disabled)
        self.assertTrue(users[1].disabled)
        self.assertEqual(users[1].id, "9a1c")

    def test_header_only(self) -> None:
        self.assertEqual(parsing.parse_users("USERNAME  GROUPS  DISABLED  ID\n"), [])

    def test_username_looks_like_header(self) -> None:
        users = parsing.parse_users(
            "USERNAME  GROUPS  DISABLED  ID\nUSERNAME  User    false     abc\n"
        )
        self.assertEqual([user.username for user in users], ["USERNAME"])

    def test_bad_row(self) -> None:
        with self.assertRaises(parsing.ParseError):
            parsing.parse_users("USERNAME GROUPS DISABLED ID\nalice only-three\n")


class ParseWhoamiTest(unittest.TestCase):
    def test_identity(self) -> None:
        identity = parsing.parse_whoami(
            "owner\tUser,Admin,Owner\t2026-11-25 22:10:00\n"
        )
        self.assertEqual(identity.username, "owner")
        self.assertEqual(identity.groups, ("User", "Admin", "Owner"))
        self.assertEqual(identity.groups_text, "User, Admin, Owner")
        self.assertEqual(identity.role, "Owner")
        self.assertEqual(identity.expires_at, datetime(2026, 11, 25, 22, 10, 0))
        self.assertTrue(identity.is_admin)
        self.assertTrue(identity.is_owner)

    def test_bad_output(self) -> None:
        with self.assertRaises(parsing.ParseError):
            parsing.parse_whoami("")
        with self.assertRaises(parsing.ParseError):
            parsing.parse_whoami("owner\tUser,Admin\n")
        with self.assertRaises(parsing.ParseError):
            parsing.parse_whoami("owner\tUser,Admin\tnot-a-time\n")
        with self.assertRaises(parsing.ParseError):
            parsing.parse_whoami("a\tUser\t2026-11-25 22:10:00\nb\tUser\t2026-11-25 22:10:00\n")


class IdentityPermissionTest(unittest.TestCase):
    def _identity(self, groups: str) -> parsing.Identity:
        return parsing.Identity(
            "me",
            tuple(groups.split(",")),
            datetime(2026, 11, 25, 22, 10, 0),
        )

    def _user(self, name: str, groups: str) -> parsing.User:
        return parsing.User(name, tuple(groups.split(",")), False, name)

    def test_owner_can_operate_everyone(self) -> None:
        owner = self._identity("User,Owner")
        for target in (
            self._user("user", "User"),
            self._user("admin", "User,Admin"),
            self._user("owner2", "User,Owner"),
            self._user("me", "User,Owner"),
        ):
            with self.subTest(target=target.username):
                self.assertTrue(owner.can_operate(target))
        self.assertFalse(owner.can_disable(self._user("me", "User,Owner")))
        self.assertFalse(owner.can_delete(self._user("me", "User,Owner")))

    def test_admin_limited_to_user(self) -> None:
        admin = self._identity("User,Admin")
        user = self._user("user", "User")
        other_admin = self._user("admin2", "User,Admin")
        owner = self._user("owner", "User,Owner")
        me = self._user("me", "User,Admin")
        for target in (user,):
            self.assertTrue(admin.can_reset_password(target))
            self.assertTrue(admin.can_disable(target))
            self.assertTrue(admin.can_delete(target))
        for target in (other_admin, owner, me):
            with self.subTest(target=target.username):
                self.assertFalse(admin.can_operate(target))
                self.assertFalse(admin.can_reset_password(target))
                self.assertFalse(admin.can_disable(target))
                self.assertFalse(admin.can_delete(target))
        self.assertFalse(admin.can_grant_groups)
        self.assertFalse(admin.can_manage_backups)


class ParseWhoamiGroupsTest(unittest.TestCase):
    def test_unknown_groups_do_not_count(self) -> None:
        identity = parsing.parse_whoami(
            "me\tUser,Ghost\t2026-11-25 22:10:00\n"
        )
        self.assertEqual(identity.rank, 1)
        self.assertEqual(identity.role, "User")


class ParseBackupTest(unittest.TestCase):

    def test_path_with_spaces(self) -> None:
        self.assertEqual(
            parsing.parse_backup_path("备份已写入 /tmp/my backups/a.zip\n"),
            "/tmp/my backups/a.zip",
        )

    def test_missing(self) -> None:
        self.assertIsNone(parsing.parse_backup_path("something else"))


class ParseBackupTimeTest(unittest.TestCase):
    def test_default_name(self) -> None:
        self.assertEqual(
            parsing.parse_backup_time(
                "/data/backups/chat-deepseek-20261123-120220.zip"
            ),
            datetime(2026, 11, 23, 12, 2, 20),
        )

    def test_windows_path(self) -> None:
        self.assertEqual(
            parsing.parse_backup_time(
                r"C:\backups\chat-deepseek-20261123-120220.zip"
            ),
            datetime(2026, 11, 23, 12, 2, 20),
        )

    def test_renamed_backup(self) -> None:
        self.assertEqual(
            parsing.parse_backup_time("my-backup-20261123-120220.zip"),
            datetime(2026, 11, 23, 12, 2, 20),
        )

    def test_invalid_or_missing_time(self) -> None:
        self.assertIsNone(parsing.parse_backup_time("chat-deepseek.zip"))
        self.assertIsNone(
            parsing.parse_backup_time("chat-deepseek-20261399-999999.zip")
        )
        self.assertIsNone(
            parsing.parse_backup_time("/backups/20261123-120220/manual.zip")
        )


class ParseBackupListTest(unittest.TestCase):
    def test_rows(self) -> None:
        text = (
            "NAME\tSIZE\tTIME\n"
            "chat-deepseek-20261123-120220.zip\t2.0 KB\t2026-11-23 12:02:20\n"
            "manual.zip\t512 B\t2026-11-22 09:00:00\n"
        )
        backups = parsing.parse_backup_list(text)
        self.assertEqual([item.name for item in backups], [
            "chat-deepseek-20261123-120220.zip",
            "manual.zip",
        ])
        self.assertEqual(backups[0].size, "2.0 KB")
        self.assertEqual(backups[0].time, datetime(2026, 11, 23, 12, 2, 20))

    def test_empty(self) -> None:
        self.assertEqual(parsing.parse_backup_list(""), [])
        self.assertEqual(parsing.parse_backup_list("NAME\tSIZE\tTIME\n"), [])

    def test_bad_row(self) -> None:
        with self.assertRaises(parsing.ParseError):
            parsing.parse_backup_list("NAME\tSIZE\tTIME\nbad\n")
        with self.assertRaises(parsing.ParseError):
            parsing.parse_backup_list("a.zip\t2.0 KB\tnot-a-time\n")


class FormatSizeTest(unittest.TestCase):
    def test_units(self) -> None:
        self.assertEqual(parsing.format_size(10), "10 B")
        self.assertEqual(parsing.format_size(2048), "2.0 KB")
        self.assertEqual(parsing.format_size(2 * 1024 * 1024), "2.0 MB")


class ParseVersionTest(unittest.TestCase):
    def test_version(self) -> None:
        self.assertEqual(
            parsing.parse_version("chat-deepseek 0.1.0-official0.1.3 linux/amd64\n"),
            ("0.1.0-official0.1.3", "linux/amd64"),
        )

    def test_bad(self) -> None:
        self.assertIsNone(parsing.parse_version("chat-deepseek\n"))


class ParseServeTest(unittest.TestCase):
    def test_events(self) -> None:
        self.assertEqual(
            parsing.parse_serve_event("chat-deepseek 正在监听 127.0.0.1:8090"),
            parsing.ServeEvent("listening", "127.0.0.1:8090"),
        )
        self.assertEqual(
            parsing.parse_serve_event("打开：http://127.0.0.1:8090"),
            parsing.ServeEvent("url", "http://127.0.0.1:8090"),
        )
        self.assertEqual(
            parsing.parse_serve_event("time=x level=INFO msg=正在关闭服务"),
            parsing.ServeEvent("stopping"),
        )
        self.assertEqual(
            parsing.parse_serve_event("time=x level=ERROR msg=boom"),
            parsing.ServeEvent("error", "time=x level=ERROR msg=boom"),
        )


class ExplainFailureTest(unittest.TestCase):
    def test_backup_while_service_running(self) -> None:
        failure = parsing.explain_failure(
            "错误： backup: database is in use：检测到 chat.db-wal，请先停止服务再备份\n",
            args=("backup",),
        )
        self.assertEqual(failure.title, "服务正在运行")
        self.assertIn("停止", failure.message)
        self.assertIsNone(failure.details)

    def test_literal_running_output(self) -> None:
        failure = parsing.explain_failure(
            "错误： 服务正在运行，无法继续\n",
            args=("backup",),
        )
        self.assertEqual(failure.title, "服务正在运行")
        self.assertIn("停止", failure.message)

    def test_port_already_in_use(self) -> None:
        failure = parsing.explain_failure(
            "错误： 监听 127.0.0.1:8090 失败: listen tcp 127.0.0.1:8090: "
            "bind: address already in use\n",
            args=("serve", "--no-browser"),
        )
        self.assertEqual(failure.title, "端口已被占用")
        self.assertIn("127.0.0.1:8090", failure.message)
        self.assertIsNone(failure.details)

    def test_invalid_listen_address(self) -> None:
        failure = parsing.explain_failure(
            "错误： 监听 nonsense 失败: listen tcp: address nonsense: "
            "missing port in address\n",
            args=("serve",),
        )
        self.assertEqual(failure.title, "监听地址无效")
        self.assertIn("host:port", failure.message)

    def test_last_owner_action_titles(self) -> None:
        error = "错误： account: cannot remove the last active Owner\n"
        self.assertEqual(
            parsing.explain_failure(error, args=("user", "delete", "owner")).title,
            "不能删除最后一个 Owner",
        )
        self.assertEqual(
            parsing.explain_failure(error, args=("user", "disable", "owner")).title,
            "不能禁用最后一个 Owner",
        )
        self.assertEqual(
            parsing.explain_failure(
                error, args=("user", "revoke", "owner", "Owner")
            ).title,
            "不能撤销最后一个 Owner 组",
        )

    def test_account_errors(self) -> None:
        cases = {
            "错误： account: user not found\n": "账户不存在",
            "错误： account: username already exists\n": "用户名已存在",
            "错误： account: cannot remove the base User group\n": "不能撤销 User 组",
        }
        for text, title in cases.items():
            with self.subTest(text=text):
                self.assertEqual(parsing.explain_failure(text).title, title)

    def test_password_errors(self) -> None:
        self.assertEqual(
            parsing.explain_failure("错误： 密码至少 8 位\n").title,
            "密码太短",
        )
        self.assertEqual(
            parsing.explain_failure("错误： 读取密码失败: EOF\n").title,
            "无法读取密码",
        )

    def test_missing_owner(self) -> None:
        failure = parsing.explain_failure(
            "错误： 还没有活动 Owner；先运行 chat-deepseek setup\n",
            args=("serve",),
        )
        self.assertEqual(failure.title, "还没有 Owner 账户")
        self.assertIn("创建 Owner", failure.message)

    def test_initialized_and_missing_database(self) -> None:
        self.assertEqual(
            parsing.explain_failure(
                "错误： 这个数据目录已经初始化过（存在活动 Owner）\n"
            ).title,
            "数据目录已初始化",
        )
        self.assertEqual(
            parsing.explain_failure(
                "错误： 数据目录中没有 chat.db：/tmp/data\n"
            ).title,
            "数据目录还没有初始化",
        )

    def test_backup_restore_errors(self) -> None:
        cases = {
            "错误： 备份不存在：gone.zip\n": "找不到备份",
            "错误： 备份中没有 chat.db\n": "备份不完整",
            "错误： 备份地址不能是数据目录本身\n": "备份地址无效",
            "错误： 打开备份 bad.zip: zip: not a valid zip file\n": "备份文件损坏",
        }
        for text, title in cases.items():
            with self.subTest(text=text):
                self.assertEqual(parsing.explain_failure(text).title, title)

    def test_missing_binary(self) -> None:
        failure = parsing.explain_failure(
            "[Errno 2] No such file or directory: '/opt/chat-deepseek/chat-deepseek'",
            args=("serve",),
        )
        self.assertEqual(failure.title, "找不到 chat-deepseek")

    def test_missing_data_file_is_not_missing_binary(self) -> None:
        failure = parsing.explain_failure(
            "错误： open /tmp/chat-deepseek/data/file: no such file or directory\n",
            args=("backup",),
        )
        self.assertEqual(failure.title, "文件或目录不存在")

    def test_permission_errors_are_distinguished(self) -> None:
        binary = parsing.explain_failure(
            "[Errno 13] Permission denied: '/opt/chat-deepseek'",
            args=("serve",),
        )
        self.assertEqual(binary.title, "无法运行 chat-deepseek")

        port = parsing.explain_failure(
            "错误： 监听 127.0.0.1:80 失败: listen tcp 127.0.0.1:80: "
            "bind: permission denied\n",
            args=("serve",),
        )
        self.assertEqual(port.title, "没有权限启动服务")
        self.assertIn("1024", port.message)

    def test_unknown_flag_names_the_flag(self) -> None:
        failure = parsing.explain_failure(
            "flag provided but not defined: -username\n"
            "错误： flag provided but not defined: -username\n",
            args=("user", "add"),
        )
        self.assertEqual(failure.title, "命令行参数无效")
        self.assertIn("-username", failure.message)

    def test_auth_required_exit_code(self) -> None:
        cases = {
            "错误： 需要先登录：chat-deepseek login\n": "需要登录",
            "错误： 登录已过期，请重新 login\n": "登录已过期",
            "错误： 登录已失效，请重新 login\n": "登录已失效",
            "错误： 当前登录的数据目录是 /a，本次命令指向 /b；请重新 login\n": "数据目录不一致",
        }
        for text, title in cases.items():
            with self.subTest(text=text):
                self.assertEqual(
                    parsing.explain_failure(text, code=3).title, title
                )

    def test_forbidden_exit_code(self) -> None:
        cases = {
            "错误： 该账户没有 CLI 管理权限\n": "没有管理权限",
            "错误： 只有 Owner 可以指定组；Admin 只能创建普通 User\n": "只有 Owner 可以指定组",
            "错误： 只有 Owner 可以操作 owner 账户\n": "权限不足",
            "错误： 不能禁用自己\n": "不能禁用自己",
            "错误： 不能删除自己\n": "不能删除自己",
            "错误： 需要 Owner 权限\n": "需要 Owner 权限",
        }
        for text, title in cases.items():
            with self.subTest(text=text):
                self.assertEqual(
                    parsing.explain_failure(text, code=4).title, title
                )

    def test_login_errors(self) -> None:
        cases = {
            "错误： 账户不存在\n": "账户不存在",
            "错误： 密码错误\n": "密码错误",
            "错误： 账户已被禁用\n": "账户已被禁用",
            "错误： 数据目录未初始化：/tmp/data；先运行 setup 或 backup restore\n": "数据目录还没有初始化",
        }
        for text, title in cases.items():
            with self.subTest(text=text):
                self.assertEqual(parsing.explain_failure(text).title, title)

    def test_unknown_error_keeps_output(self) -> None:
        failure = parsing.explain_failure(
            "错误： 奇怪的失败\n",
            "some stdout\n",
            ("user", "list"),
            7,
        )
        self.assertEqual(failure.title, "未知错误")
        self.assertIn("无法识别", failure.message)
        self.assertIsNotNone(failure.details)
        details = failure.details or ""
        self.assertIn("退出码：7", details)
        self.assertIn("奇怪的失败", details)
        self.assertIn("some stdout", details)

    def test_no_output_unknown(self) -> None:
        failure = parsing.explain_failure("", "", ("user", "list"), 1)
        self.assertEqual(failure.title, "未知错误")
        self.assertEqual(failure.details, "退出码：1")


class ServeFailureTest(unittest.TestCase):
    def test_known_reason(self) -> None:
        failure = parsing.explain_serve_failure(
            [
                "time=x level=INFO msg=正在监听 127.0.0.1:8090",
                "错误： 监听 127.0.0.1:8090 失败: listen tcp 127.0.0.1:8090: "
                "bind: address already in use",
            ],
            1,
        )
        self.assertEqual(failure.title, "端口已被占用")
        self.assertIsNone(failure.details)

    def test_unknown_keeps_log(self) -> None:
        failure = parsing.explain_serve_failure(
            ["错误： 奇怪的失败"], 2
        )
        self.assertEqual(failure.title, "未知错误")
        self.assertIn("退出码 2", failure.message)
        self.assertIsNotNone(failure.details)
        self.assertIn("奇怪的失败", failure.details or "")


if __name__ == "__main__":
    unittest.main()
