"""选择系统 UI 字体。

不枚举字体列表（Windows 上枚举要几百毫秒），只按平台给出首选字体，
再交给 Qt 做 fallback。缺字体时 Qt 会自动回退，不会影响启动。
"""

from __future__ import annotations

import sys


def family() -> str:
    if sys.platform == "win32":
        return "Microsoft YaHei UI"
    if sys.platform == "darwin":
        return "PingFang SC"
    return "Noto Sans CJK SC"


def stack() -> list[str]:
    """首选字体之后的中文 / 西文回退顺序。"""
    return [
        family(),
        "Noto Sans CJK SC",
        "Source Han Sans SC",
        "WenQuanYi Micro Hei",
        "Segoe UI",
        "Helvetica Neue",
        "sans-serif",
    ]
