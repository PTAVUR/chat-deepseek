"""GUI 本地设置（config.gui.json）。

服务端配置仍写在数据目录的 config.json；这里只保存 GUI 自己的路径，
因为数据目录本身不能存在数据目录里的配置中。
"""

from __future__ import annotations

import json
import os
import sys
import tempfile
from pathlib import Path

KEYS = ("binary", "data_dir", "backup_dir")


def config_dir() -> Path:
    """返回当前平台的用户配置目录。"""
    if sys.platform == "win32":
        base = os.environ.get("APPDATA")
        directory = Path(base) if base else Path.home() / "AppData" / "Roaming"
    elif sys.platform == "darwin":
        directory = Path.home() / "Library" / "Application Support"
    else:
        base = os.environ.get("XDG_CONFIG_HOME")
        directory = Path(base) if base else Path.home() / ".config"
    return directory / "chat-deepseek"


def default_path() -> Path:
    """返回 config.gui.json 的平台标准路径。"""
    return config_dir() / "config.gui.json"


def seed_paths() -> list[Path]:
    """返回 macOS .app 内和 DMG 根目录的只读配置模板。"""
    if sys.platform != "darwin" or not getattr(sys, "frozen", False):
        return []
    executable = Path(sys.executable).resolve()
    if len(executable.parents) < 4:
        return []
    resources = executable.parents[1] / "Resources" / "config.gui.json"
    root = executable.parents[3] / "config.gui.json"
    return [resources, root]


def load(path: str | Path) -> dict[str, str]:
    """读取 GUI 配置；缺失、损坏或类型不对时返回空配置。"""
    path = Path(path).expanduser()
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return {}
    if not isinstance(raw, dict):
        return {}
    result: dict[str, str] = {}
    for key in KEYS:
        value = raw.get(key)
        if isinstance(value, str) and value.strip():
            result[key] = value.strip()
    return result


def save(path: str | Path, values: dict[str, str]) -> None:
    """原子写入 GUI 配置，权限保持 0600。"""
    path = Path(path).expanduser()
    path.parent.mkdir(parents=True, exist_ok=True)
    payload = {key: str(values.get(key, "")).strip() for key in KEYS}
    fd, temporary = tempfile.mkstemp(prefix=path.name + ".", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as handle:
            json.dump(payload, handle, ensure_ascii=False, indent=2)
            handle.write("\n")
        os.chmod(temporary, 0o600)
        os.replace(temporary, path)
    except BaseException:
        try:
            os.unlink(temporary)
        except OSError:
            pass
        raise
