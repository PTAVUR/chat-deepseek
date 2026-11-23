"""解析 chat-deepseek 的中文 CLI 输出。

所有对 CLI 文案的依赖都集中在这个文件；CLI 改文案时只改这里。
正常路径尽量用退出码判断；失败输出由 explain_failure 翻译成可操作提示，
识别不了才回退到原始输出。成功输出解析失败时由界面显示“未知错误”。
"""

from __future__ import annotations

import re
from collections.abc import Sequence
from dataclasses import dataclass


class ParseError(ValueError):
    """CLI 输出不符合预期格式。"""


@dataclass(frozen=True)
class Failure:
    """可以直接展示的失败说明。

    已知条件要给出下一步操作，不再把 CLI 原始输出丢给用户；只有完全无法识别的
    输出才放在 details 里，供排查使用。
    """

    title: str
    message: str
    details: str | None = None


@dataclass(frozen=True)
class User:
    username: str
    groups: tuple[str, ...]
    disabled: bool
    id: str

    @property
    def groups_text(self) -> str:
        return ", ".join(self.groups)

    @property
    def status_text(self) -> str:
        return "已禁用" if self.disabled else "正常"


@dataclass(frozen=True)
class ServeEvent:
    kind: str
    value: str = ""


def parse_users(text: str) -> list[User]:
    """解析 `user list` 的 tabwriter 表格。"""
    users: list[User] = []
    for line in text.splitlines():
        line = line.strip()
        if not line:
            continue
        parts = line.split()
        if parts[:2] == ["USERNAME", "GROUPS"]:
            continue
        if len(parts) != 4:
            raise ParseError(f"无法解析用户行：{line}")
        username, groups_text, disabled_text, user_id = parts
        if disabled_text not in ("true", "false"):
            raise ParseError(f"无法解析用户状态：{line}")
        groups = tuple(group for group in groups_text.split(",") if group)
        users.append(
            User(
                username=username,
                groups=groups,
                disabled=disabled_text == "true",
                id=user_id,
            )
        )
    return users


def parse_backup_path(text: str) -> str | None:
    """从 `备份已写入 <path>` 取出路径；没有该行时返回 None。"""
    match = re.search(r"备份已写入\s+(.+?)\s*$", text, re.MULTILINE)
    return match.group(1).strip() if match else None


def parse_version(text: str) -> tuple[str, str] | None:
    """解析 `chat-deepseek <version> <goos>/<goarch>`。"""
    match = re.search(r"chat-deepseek\s+(\S+)\s+(\S+)", text)
    return (match.group(1), match.group(2)) if match else None


def parse_serve_event(line: str) -> ServeEvent:
    """识别 serve 的关键日志；其余行作为普通日志返回。"""
    if match := re.search(r"正在监听\s+(\S+)", line):
        return ServeEvent("listening", match.group(1))
    if line.startswith("打开："):
        return ServeEvent("url", line[len("打开：") :].strip())
    if "正在关闭服务" in line:
        return ServeEvent("stopping")
    if "level=ERROR" in line or line.startswith("错误：") or line.startswith("错误:"):
        return ServeEvent("error", line)
    return ServeEvent("log", line)


def unknown_output(
    message: str,
    stdout: str = "",
    stderr: str = "",
    code: int | None = None,
) -> Failure:
    """构造“无法解析输出”的兜底提示，原始输出放进 details。"""
    return Failure("未知错误", message, _raw_output(stderr, stdout, code))


def explain_failure(
    stderr: str,
    stdout: str = "",
    args: Sequence[str] = (),
    code: int | None = None,
) -> Failure:
    """把 CLI 失败翻译成用户能照着处理的提示。

    识别不了的条件返回“未知错误”，并把 stderr/stdout 原样作为 details，避免
    悄悄吞掉真正有用的信息。
    """
    combined = "\n".join(part for part in (stderr, stdout) if part.strip())
    lowered = combined.lower()
    command = args[0].lower() if args else ""
    subcommand = args[1].lower() if len(args) > 1 else ""
    address = _listen_address(combined)

    missing_binary = re.search(
        r"(?:no such file or directory|cannot find the file specified|"
        r"系统找不到指定的文件)[^\n]*chat-deepseek(?:\.exe)?['\"]?\s*$",
        combined,
        re.IGNORECASE | re.MULTILINE,
    ) or (
        "chat-deepseek" in lowered
        and "executable file not found" in lowered
    )
    if missing_binary:
        return Failure(
            "找不到 chat-deepseek",
            "请到设置里选择 chat-deepseek 所在的二进制目录。",
        )

    if any(
        marker in lowered
        for marker in (
            "database is in use",
            "database is locked",
            "database is busy",
            "sqlite_busy",
            "chat.db-wal",
            "chat.db-shm",
            "请先停止服务",
            "already running",
            "server is running",
            "service is running",
            "正在运行",
            "运行中",
        )
    ):
        return Failure(
            "服务正在运行",
            "请先停止 chat-deepseek 服务，再重试这个操作。",
        )

    if any(
        marker in lowered
        for marker in (
            "address already in use",
            "only one usage of each socket address",
            "eaddrinuse",
            "winerror 10048",
        )
    ):
        where = f"监听地址 {address} 已被占用" if address else "监听地址已被占用"
        return Failure(
            "端口已被占用",
            f"{where}。请关闭正在运行的服务，或换一个端口后重试。",
        )

    if any(
        marker in lowered
        for marker in (
            "permission denied",
            "operation not permitted",
            "access is denied",
            "winerror 5",
            "拒绝访问",
            "权限不足",
        )
    ):
        if "chat-deepseek" in lowered:
            return Failure(
                "无法运行 chat-deepseek",
                "文件没有执行权限。请检查二进制文件权限，或重新选择二进制目录。",
            )
        if command == "serve" and ("listen" in lowered or "bind" in lowered):
            return Failure(
                "没有权限启动服务",
                "请改用 1024 以上的端口，或使用有权限的账户运行。",
            )
        return Failure(
            "没有访问权限",
            "无法读写数据目录或备份路径，请检查目录权限后重试。",
        )

    if any(
        marker in lowered
        for marker in (
            "read-only file system",
            "readonly database",
            "attempt to write a readonly database",
        )
    ):
        return Failure(
            "数据目录不可写",
            "请确认当前用户对数据目录有写权限后重试。",
        )

    if any(
        marker in lowered
        for marker in (
            "no space left",
            "disk full",
            "not enough space",
            "磁盘空间不足",
        )
    ):
        return Failure("磁盘空间不足", "请清理磁盘空间后再试。")

    if any(
        marker in lowered
        for marker in (
            "missing port in address",
            "too many colons in address",
            "invalid port",
            "no such host",
            "name resolution",
            "cannot assign requested address",
            "address family not supported",
        )
    ):
        where = f"监听地址 {address} 无法使用" if address else "监听地址无法使用"
        return Failure(
            "监听地址无效",
            f"{where}。请在设置里填写 host:port 形式的地址，例如 127.0.0.1:8090。",
        )

    if "还没有活动 owner" in lowered:
        return Failure(
            "还没有 Owner 账户",
            "请先在主界面创建 Owner 账户，再启动服务。"
            if command == "serve"
            else "请先创建 Owner 账户。",
        )

    if "已经初始化过" in combined:
        return Failure(
            "数据目录已初始化",
            "这个数据目录里已经有 Owner。可以直接添加账户，或换一个数据目录。",
        )

    if "数据目录中没有 chat.db" in combined or "没有 chat.db" in combined:
        return Failure(
            "数据目录还没有初始化",
            "这个目录里没有 chat.db。请先创建 Owner 账户，或检查设置里的数据目录。",
        )

    if "备份输出不能覆盖数据文件" in combined:
        return Failure(
            "备份路径无效",
            "备份文件不能覆盖数据目录里的 chat.db、master.key 或 config.json，请换一个路径。",
        )

    if "parse config" in lowered or "读取 config" in combined:
        return Failure(
            "配置文件无法读取",
            "data/config.json 内容可能已损坏，请检查或备份后删除该文件再试。",
        )
    if "write config" in lowered or "replace config" in lowered:
        return Failure(
            "配置文件无法保存",
            "无法写入 data/config.json，请检查数据目录权限后重试。",
        )

    if "cannot remove the last active owner" in lowered:
        if subcommand == "delete":
            title = "不能删除最后一个 Owner"
            action = "删除这个账户"
        elif subcommand == "disable":
            title = "不能禁用最后一个 Owner"
            action = "禁用这个账户"
        elif subcommand == "revoke":
            title = "不能撤销最后一个 Owner 组"
            action = "撤销它的 Owner 组"
        else:
            title = "不能移除最后一个 Owner"
            action = "移除它"
        return Failure(
            title,
            f"请先把 Owner 授予另一个账户，再{action}。",
        )

    if "cannot remove the base user group" in lowered:
        return Failure(
            "不能撤销 User 组",
            "User 是所有账户的基础组，不能撤销。",
        )

    if "username already exists" in lowered:
        return Failure("用户名已存在", "换一个用户名再试。")

    if "user not found" in lowered:
        return Failure(
            "账户不存在",
            "这个账户可能已经被删除，刷新列表后再试。",
        )

    if "invalid username" in lowered or "用户名需为" in combined:
        return Failure(
            "用户名无效",
            "用户名只能包含字母、数字和 . _ -，长度为 1–64 位。",
        )

    if "invalid group" in lowered or "未知组" in combined:
        return Failure("未知的组", "可用组只有 User、Admin 和 Owner。")

    if "密码至少 8 位" in combined:
        return Failure("密码太短", "密码至少需要 8 位，请重新输入。")
    if "两次输入的密码不一致" in combined:
        return Failure("两次密码不一致", "请重新输入并确认两次密码相同。")
    if "密码不符合要求" in combined:
        return Failure("密码不符合要求", "请换一个密码后重试。")
    if any(
        marker in combined
        for marker in ("读取密码失败", "读取输入失败", "标准输入不是终端")
    ):
        return Failure(
            "无法读取密码",
            "密码没有成功传给 chat-deepseek，请重新打开输入框再试。",
        )

    if "flag provided but not defined" in lowered or "unknown flag" in lowered:
        match = re.search(
            r"(?:flag provided but not defined|unknown flag):?\s*(\S+)",
            combined,
            re.IGNORECASE,
        )
        flag = f" {match.group(1)}" if match else ""
        return Failure(
            "命令行参数无效",
            f"当前 chat-deepseek 不支持参数{flag}，可能是版本不匹配。请更新二进制后重试。",
        )

    if any(
        marker in lowered
        for marker in ("flag needs an argument", "invalid value", "bad flag syntax")
    ):
        return Failure(
            "命令行参数无效",
            "界面传递给 chat-deepseek 的参数不完整，请确认二进制版本。",
        )

    if "未知命令" in combined or "未知 user 子命令" in combined:
        return Failure(
            "命令不可用",
            "当前 chat-deepseek 不支持这个命令，可能是界面与二进制版本不匹配。",
        )

    if "不接受位置参数" in combined or "用法：" in combined:
        return Failure(
            "命令行参数无效",
            "界面传递的参数与 chat-deepseek 不匹配，请确认二进制版本。",
        )

    if any(
        marker in lowered
        for marker in ("no such file or directory", "cannot find the file specified")
    ):
        return Failure(
            "文件或目录不存在",
            "请检查数据目录和备份路径是否仍然存在。",
        )

    return Failure(
        "未知错误",
        "chat-deepseek 返回了无法识别的结果。",
        _raw_output(stderr, stdout, code),
    )


def explain_serve_failure(lines: Sequence[str], code: int) -> Failure:
    """从 serve 的最后几行日志里找原因；找不到就保留完整日志。"""
    text = "\n".join(line for line in lines if line.strip())
    failure = explain_failure(text, "", ("serve",))
    if failure.details is None:
        return failure
    return Failure(
        "未知错误",
        f"chat-deepseek 服务意外退出（退出码 {code}）。",
        f"退出码：{code}\n\n服务日志：\n{text.strip()}" if text.strip() else f"退出码：{code}",
    )


def _listen_address(text: str) -> str:
    match = re.search(r"监听\s+(\S+)\s+失败", text)
    return match.group(1).strip() if match else ""


def _raw_output(stderr: str, stdout: str, code: int | None) -> str:
    parts: list[str] = []
    if code is not None:
        parts.append(f"退出码：{code}")
    stderr = stderr.strip()
    stdout = stdout.strip()
    if stderr:
        parts.append("标准错误：\n" + stderr)
    if stdout:
        parts.append("标准输出：\n" + stdout)
    return "\n\n".join(parts) or "没有输出。"
