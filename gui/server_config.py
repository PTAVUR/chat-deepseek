"""读取服务端 config.json，并给出与 CLI 一致的默认值。"""

from __future__ import annotations

import json
import os
import sys
import tempfile
from pathlib import Path

DEFAULT_LISTEN = "127.0.0.1:8090"
DEFAULT_API_ORIGIN = "https://api.deepseek.com"


def default_data_dir(binary_dir: str | Path) -> Path:
    """返回默认数据目录；macOS 的应用包在 /Applications 下通常不可写。"""
    directory = Path(binary_dir).expanduser()
    if sys.platform == "darwin":
        return Path.home() / "Library" / "Application Support" / "chat-deepseek" / "data"
    return directory / "data"


def defaults(binary_dir: str | Path) -> dict[str, str]:
    """按二进制目录计算一套默认参数。"""
    directory = Path(binary_dir).expanduser()
    data_dir = default_data_dir(directory)
    return {
        "binary": str(directory),
        "data_dir": str(data_dir),
        "backup_dir": str(data_dir / "backups"),
        "listen": DEFAULT_LISTEN,
        "public_url": "",
        "api_origin": DEFAULT_API_ORIGIN,
    }


def load(data_dir: str | Path) -> dict[str, str]:
    """读取数据目录里的 config.json；缺失或损坏时回退到默认值。"""
    result = {
        "listen": DEFAULT_LISTEN,
        "public_url": "",
        "api_origin": DEFAULT_API_ORIGIN,
    }
    path = Path(data_dir).expanduser() / "config.json"
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return result
    if not isinstance(raw, dict):
        return result
    for key in result:
        value = raw.get(key)
        if isinstance(value, str) and value.strip():
            result[key] = value.strip()
    return result


def save(data_dir: str | Path, values: dict[str, str]) -> None:
    """把 listen/public_url/api_origin 原子写入数据目录的 config.json。

    已有的未知字段会保留，避免 GUI 覆盖未来版本新增的配置项。
    """
    directory = Path(data_dir).expanduser()
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    path = directory / "config.json"
    raw: dict[str, object] = {}
    try:
        existing = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        existing = None
    if isinstance(existing, dict):
        raw.update(existing)
    for key in ("listen", "public_url", "api_origin"):
        value = values.get(key)
        if isinstance(value, str):
            raw[key] = value.strip()

    fd, temporary = tempfile.mkstemp(prefix=path.name + ".", dir=directory)
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as handle:
            json.dump(raw, handle, ensure_ascii=False, indent=2)
            handle.write("\n")
        os.chmod(temporary, 0o600)
        os.replace(temporary, path)
    except BaseException:
        try:
            os.unlink(temporary)
        except OSError:
            pass
        raise
