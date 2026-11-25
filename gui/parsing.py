"""解析 chat-deepseek 的中文 CLI 输出。

所有对 CLI 文案的依赖都集中在这个文件；CLI 改文案时只改这里。
正常路径尽量用退出码判断；失败输出由 explain_failure 翻译成可操作提示，
识别不了才回退到原始输出。成功输出解析失败时由界面显示“未知错误”。
"""

from __future__ import annotations

import re
from collections.abc import Sequence
from dataclasses import dataclass
from datetime import datetime


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

    @property
    def rank(self) -> int:
        return group_rank(self.groups)


GROUP_RANKS = {"User": 1, "Admin": 2, "Owner": 3}


def group_rank(groups: Sequence[str]) -> int:
    """返回账户组里的最高权限等级；未知组忽略。"""
    return max((GROUP_RANKS.get(group, 0) for group in groups), default=0)


@dataclass(frozen=True)
class Identity:
    """`whoami` 返回的当前 CLI 登录身份。"""

    username: str
    groups: tuple[str, ...]
    expires_at: datetime

    @property
    def groups_text(self) -> str:
        return ", ".join(self.groups)

    @property
    def rank(self) -> int:
        return group_rank(self.groups)

    @property
    def role(self) -> str:
        rank = self.rank
        for name, value in GROUP_RANKS.items():
            if value == rank:
                return name
        return "User"

    @property
    def is_admin(self) -> bool:
        return self.rank >= GROUP_RANKS["Admin"]

    @property
    def is_owner(self) -> bool:
        return self.rank >= GROUP_RANKS["Owner"]

    def can_operate(self, target: User) -> bool:
        """与 CLI 的 canOperate 一致：Owner 可以操作任何账户，其余要求严格高于目标。"""
        return self.is_owner or target.rank < self.rank

    def can_reset_password(self, target: User) -> bool:
        return self.can_operate(target)

    def can_disable(self, target: User) -> bool:
        return self.can_operate(target) and target.username != self.username

    def can_delete(self, target: User) -> bool:
        return self.can_operate(target) and target.username != self.username

    @property
    def can_grant_groups(self) -> bool:
        return self.is_owner

    @property
    def can_manage_backups(self) -> bool:
        return self.is_owner


@dataclass(frozen=True)
class Backup:
    name: str
    size: str
    time: datetime


@dataclass(frozen=True)
class ServeEvent:
    kind: str
    value: str = ""


@dataclass(frozen=True)
class Info:
    """`chat-deepseek info` 的只读快照。"""

    version: str
    os: str
    arch: str
    binary: str
    data_dir: str
    initialized: bool
    listen: str
    public_url: str
    api_origin: str
    backup_dir: str
    service_supported: bool
    service_scope: str
    service_installed: bool
    service_active: bool
    service_enabled: bool
    service_unit: str
    service_linger: bool


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


def parse_whoami(text: str) -> Identity:
    """解析 `whoami` 的 tab 分隔输出：用户名、组、过期时间。"""
    lines = [line for line in text.splitlines() if line.strip()]
    if len(lines) != 1:
        raise ParseError("whoami 输出应为一行")
    parts = lines[0].split("\t")
    if len(parts) != 3:
        raise ParseError(f"无法解析登录身份：{lines[0]}")
    username, groups_text, expires_text = (part.strip() for part in parts)
    if not username:
        raise ParseError("登录身份缺少用户名")
    groups = tuple(group for group in groups_text.split(",") if group)
    try:
        expires_at = datetime.strptime(expires_text, "%Y-%m-%d %H:%M:%S")
    except ValueError as exc:
        raise ParseError(f"无法解析登录过期时间：{expires_text}") from exc
    return Identity(username=username, groups=groups, expires_at=expires_at)


def parse_backup_path(text: str) -> str | None:
    """从 `备份已写入 <path>` 取出路径；没有该行时返回 None。"""
    match = re.search(r"备份已写入\s+(.+?)\s*$", text, re.MULTILINE)
    return match.group(1).strip() if match else None


def parse_backup_time(path: str) -> datetime | None:
    """从备份文件名里取 `YYYYMMDD-HHMMSS`；不在文件名里或不合法时返回 None。

    只看最后一段文件名，避免把上级目录里的数字误当成备份时间。
    """
    name = re.split(r"[\\/]", path)[-1]
    match = re.search(r"\d{8}-\d{6}", name)
    if match is None:
        return None
    try:
        return datetime.strptime(match.group(0), "%Y%m%d-%H%M%S")
    except ValueError:
        return None


def parse_backup_list(text: str) -> list[Backup]:
    """解析 `backup list` 的 TSV 输出。"""
    backups: list[Backup] = []
    for line in text.splitlines():
        line = line.rstrip("\r")
        if not line.strip() or line == "NAME\tSIZE\tTIME":
            continue
        parts = line.split("\t")
        if len(parts) != 3:
            raise ParseError(f"无法解析备份行：{line}")
        name, size_text, time_text = parts
        try:
            stamp = datetime.strptime(time_text, "%Y-%m-%d %H:%M:%S")
        except ValueError as exc:
            raise ParseError(f"无法解析备份行：{line}") from exc
        if not name or not size_text:
            raise ParseError(f"无法解析备份行：{line}")
        backups.append(Backup(name=name, size=size_text, time=stamp))
    return backups


def format_size(size: int) -> str:
    """把字节数格式化成习惯的 KB/MB。"""
    if size >= 1024 * 1024:
        return f"{size / 1024 / 1024:.1f} MB"
    if size >= 1024:
        return f"{size / 1024:.1f} KB"
    return f"{size} B"


def parse_version(text: str) -> tuple[str, str] | None:
    """解析 `chat-deepseek <version> <goos>/<goarch>`。"""
    match = re.search(r"chat-deepseek\s+(\S+)\s+(\S+)", text)
    return (match.group(1), match.group(2)) if match else None


INFO_KEYS = (
    "version",
    "os",
    "arch",
    "binary",
    "data_dir",
    "initialized",
    "listen",
    "public_url",
    "api_origin",
    "backup_dir",
    "service_supported",
    "service_scope",
    "service_installed",
    "service_active",
    "service_enabled",
    "service_unit",
    "service_linger",
)

INFO_BOOL_KEYS = (
    "initialized",
    "service_supported",
    "service_installed",
    "service_active",
    "service_enabled",
    "service_linger",
)


def parse_info(text: str) -> Info:
    """解析 `info` 的固定 key=value 输出。"""
    values: dict[str, str] = {}
    for line in text.splitlines():
        if not line.strip():
            continue
        key, separator, value = line.partition("=")
        if not separator:
            raise ParseError(f"无法解析 info 行：{line}")
        values[key.strip()] = value.strip()
    missing = [key for key in INFO_KEYS if key not in values]
    if missing:
        raise ParseError(f"info 缺少字段：{', '.join(missing)}")

    flags: dict[str, bool] = {}
    for key in INFO_BOOL_KEYS:
        value = values[key]
        if value not in ("true", "false"):
            raise ParseError(f"无法解析 info 布尔值：{key}={value}")
        flags[key] = value == "true"

    required = ("version", "os", "arch", "binary", "data_dir", "listen", "api_origin", "backup_dir")
    for key in required:
        if not values[key]:
            raise ParseError(f"info 字段为空：{key}")

    return Info(
        version=values["version"],
        os=values["os"],
        arch=values["arch"],
        binary=values["binary"],
        data_dir=values["data_dir"],
        initialized=flags["initialized"],
        listen=values["listen"],
        public_url=values["public_url"],
        api_origin=values["api_origin"],
        backup_dir=values["backup_dir"],
        service_supported=flags["service_supported"],
        service_scope=values["service_scope"],
        service_installed=flags["service_installed"],
        service_active=flags["service_active"],
        service_enabled=flags["service_enabled"],
        service_unit=values["service_unit"],
        service_linger=flags["service_linger"],
    )


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

    if code == 3:
        if "登录已过期" in combined:
            return Failure("登录已过期", "登录有效期已到，请重新登录。")
        if "登录已失效" in combined:
            return Failure(
                "登录已失效",
                "账户可能已被禁用、删除或重置密码，请重新登录。",
            )
        if "当前登录的数据目录是" in combined:
            return Failure(
                "数据目录不一致",
                "当前登录态属于另一个数据目录，请重新登录。",
            )
        return Failure("需要登录", "请先登录 chat-deepseek CLI 管理账户。")

    if code == 4:
        if "该账户没有 CLI 管理权限" in combined:
            return Failure(
                "没有管理权限",
                "CLI 管理只对 Admin 和 Owner 开放，请换一个有权限的账户登录。",
            )
        if "只有 Owner 可以指定组" in combined:
            return Failure(
                "只有 Owner 可以指定组",
                "Admin 只能创建普通 User；需要创建 Admin 或 Owner 时请用 Owner 登录。",
            )
        if "只有 Owner 可以操作" in combined:
            return Failure(
                "权限不足",
                "只有 Owner 可以操作 Admin 或 Owner 账户。",
            )
        if "不能禁用自己" in combined:
            return Failure("不能禁用自己", "请换一个 Owner 或 Admin 账户来禁用当前账户。")
        if "不能删除自己" in combined:
            return Failure("不能删除自己", "请换一个 Owner 账户来删除当前账户。")
        if "需要 Owner 权限" in combined:
            return Failure("需要 Owner 权限", "这个操作只对 Owner 开放。")
        if "需要 Admin 或 Owner 权限" in combined:
            return Failure("需要 Admin 或 Owner 权限", "这个操作只对 Admin 和 Owner 开放。")
        return Failure("权限不足", "当前账户没有执行这个操作的权限。")

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

    if "当前平台不支持 systemd 服务" in combined or "没有检测到运行中的 systemd" in combined:
        return Failure(
            "当前平台不支持 systemd 服务",
            "继续用“启动服务”以前台会话运行；需要常驻时用 tmux/nohup、"
            "任务计划程序（Windows）或 LaunchAgent（macOS）。",
        )

    if "无法连接 systemd" in combined or "failed to connect to bus" in lowered:
        return Failure(
            "无法连接 systemd",
            "确认系统由 systemd 启动，并且当前用户有 user manager；"
            "否则改用“启动服务”以前台会话运行。",
        )

    if "unit 已存在" in combined:
        return Failure(
            "systemd 服务已存在",
            "刷新服务状态后再操作；需要覆盖 unit 时用 CLI 执行 service install --force。",
        )

    if "服务未安装" in combined:
        return Failure(
            "systemd 服务未安装",
            "先点“安装服务”，或继续用“启动服务”以前台会话运行。",
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

    if (
        "数据目录中没有 chat.db" in combined
        or "数据目录未初始化" in combined
        or "数据目录尚未初始化" in combined
    ):
        return Failure(
            "数据目录还没有初始化",
            "这个目录里没有 chat.db。请先创建 Owner 账户，或检查设置里的数据目录。",
        )

    if "备份输出不能覆盖数据文件" in combined:
        return Failure(
            "备份路径无效",
            "备份文件不能覆盖数据目录里的 chat.db、master.key 或 config.json，请换一个路径。",
        )

    if "备份地址不能是数据目录本身" in combined:
        return Failure(
            "备份地址无效",
            "备份地址不能和数据目录相同，请在设置里换一个目录。",
        )

    if "not a directory" in lowered or "不是目录" in combined:
        return Failure(
            "备份地址无效",
            "备份地址应该是一个目录，请在设置里检查。",
        )

    if "备份不存在" in combined:
        return Failure(
            "找不到备份",
            "备份可能已经被移动或删除，刷新备份记录后再试。",
        )

    if "备份不是文件" in combined:
        return Failure("备份不可用", "这个路径不是 ZIP 文件，请选择具体的备份。")

    if "备份中没有 chat.db" in combined:
        return Failure(
            "备份不完整",
            "这个备份里没有 chat.db，无法恢复。",
        )

    if "备份包含非法路径" in combined or "备份包含不支持的文件类型" in combined:
        return Failure(
            "备份不可用",
            "备份内容不合法，已停止恢复；当前数据没有被改动。",
        )

    if "打开备份" in combined and (
        "zip" in lowered or "not a valid" in lowered or "invalid" in lowered
    ):
        return Failure(
            "备份文件损坏",
            "无法读取这个 ZIP，换一个备份再试。",
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

    if "账户不存在" in combined:
        return Failure("账户不存在", "请检查用户名，或请管理员确认账户是否存在。")
    if "密码错误" in combined:
        return Failure("密码错误", "密码不正确，请重新输入。")
    if "账户已被禁用" in combined:
        return Failure("账户已被禁用", "请联系 Owner 启用这个账户后再登录。")
    if "密码过长" in combined:
        return Failure("密码过长", "密码长度不能超过 1024 位。")
    if "无法确定当前 shell 进程" in combined:
        return Failure(
            "无法保存登录态",
            "当前进程结构无法绑定登录态，请在普通终端里重新登录。",
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

    if command == "service" and any(
        marker in lowered for marker in ("systemctl", "loginctl")
    ):
        return Failure(
            "systemd 操作失败",
            "服务命令没有完成，请确认 unit 配置、systemd 用户管理器和目录权限后重试。",
            combined.strip(),
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
