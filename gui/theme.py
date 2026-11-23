"""配色、字体与全局样式表。

颜色按明暗两套 token 组织；QSS 里只引用 token，不在控件里散落色值。
"""

from __future__ import annotations

from PySide6.QtCore import Qt
from PySide6.QtGui import QColor, QFont, QGuiApplication, QPalette
from PySide6.QtWidgets import QGraphicsDropShadowEffect, QWidget

import fonts

LIGHT = {
    "bg": "#F4F4F5",
    "card": "#FFFFFF",
    "card_alt": "#F5F5F6",
    "border": "#E5E5E7",
    "border_strong": "#D4D4D8",
    "text": "#111113",
    "muted": "#6E6E78",
    "muted_soft": "#F0F0F2",
    "selected": "#E8EEF9",
    "primary": "#4D6BFE",
    "primary_hover": "#3D5AF2",
    "primary_pressed": "#3247D6",
    "primary_soft": "#EEF2FF",
    "success": "#15803D",
    "success_soft": "#ECFDF3",
    "warning": "#B45309",
    "warning_soft": "#FEF6E7",
    "danger": "#DC2626",
    "danger_hover": "#B91C1C",
    "danger_soft": "#FEF2F2",
    "switch_off": "#D4D4D8",
    "switch_off_hover": "#BEBEC5",
    "shadow": (0, 0, 0, 24),
    "overlay": "rgba(17, 17, 19, 0.45)",
}

DARK = {
    "bg": "#0A0A0B",
    "card": "#151517",
    "card_alt": "#1D1D20",
    "border": "#2B2B2F",
    "border_strong": "#414146",
    "text": "#FAFAFA",
    "muted": "#A1A1AA",
    "muted_soft": "#252528",
    "selected": "#232B3C",
    "primary": "#5B76FF",
    "primary_hover": "#6E86FF",
    "primary_pressed": "#4B63E8",
    "primary_soft": "#1C2440",
    "success": "#4ADE80",
    "success_soft": "#10231A",
    "warning": "#FBBF24",
    "warning_soft": "#2A2112",
    "danger": "#F87171",
    "danger_hover": "#FCA5A5",
    "danger_soft": "#2A1517",
    "switch_off": "#3F3F46",
    "switch_off_hover": "#52525B",
    "shadow": (0, 0, 0, 150),
    "overlay": "rgba(0, 0, 0, 0.55)",
}


def is_dark() -> bool:
    application = QGuiApplication.instance()
    if application is None:
        return False
    hints = application.styleHints()
    try:
        scheme = hints.colorScheme()
    except AttributeError:  # Qt < 6.5
        scheme = None
    if scheme == Qt.ColorScheme.Dark:
        return True
    if scheme == Qt.ColorScheme.Light:
        return False
    # 某些 Linux 平台主题不上报 colorScheme，退回窗口底色判断。
    window = application.palette().color(QPalette.ColorRole.Window)
    return window.isValid() and window.lightness() < 128


def colors() -> dict[str, object]:
    return DARK if is_dark() else LIGHT


def color(name: str) -> QColor:
    value = colors()[name]
    if isinstance(value, tuple):
        return QColor(*value)
    return QColor(str(value))


def rgba(name: str, alpha: int) -> QColor:
    base = color(name)
    base.setAlpha(alpha)
    return base


def font(size: int, weight: int = 400) -> QFont:
    value = QFont()
    value.setFamilies(fonts.stack())
    value.setPixelSize(size + 1)
    value.setWeight(QFont.Weight(weight))
    value.setStyleStrategy(
        QFont.StyleStrategy.PreferAntialias | QFont.StyleStrategy.NoSubpixelAntialias
    )
    value.setHintingPreference(QFont.HintingPreference.PreferNoHinting)
    return value


def shadow(
    widget: QWidget, blur: int = 30, dy: int = 8, alpha: int = 70
) -> QGraphicsDropShadowEffect:
    """给卡片挂一层柔和投影。QSS 没有 box-shadow，只能用图形特效。"""
    r, g, b, _ = colors()["shadow"]  # type: ignore[misc]
    if is_dark():
        alpha = max(alpha, 150)
    effect = QGraphicsDropShadowEffect(widget)
    effect.setBlurRadius(blur)
    effect.setOffset(0, dy)
    effect.setColor(QColor(r, g, b, alpha))
    widget.setGraphicsEffect(effect)
    return effect


def palette() -> QPalette:
    """把明暗 token 同步给 Qt 原生控件与弹窗。"""
    c = colors()
    result = QPalette()
    result.setColor(QPalette.ColorRole.Window, QColor(str(c["bg"])))
    result.setColor(QPalette.ColorRole.WindowText, QColor(str(c["text"])))
    result.setColor(QPalette.ColorRole.Base, QColor(str(c["card"])))
    result.setColor(QPalette.ColorRole.AlternateBase, QColor(str(c["card_alt"])))
    result.setColor(QPalette.ColorRole.Text, QColor(str(c["text"])))
    result.setColor(QPalette.ColorRole.Button, QColor(str(c["card"])))
    result.setColor(QPalette.ColorRole.ButtonText, QColor(str(c["text"])))
    result.setColor(QPalette.ColorRole.Highlight, QColor(str(c["primary"])))
    result.setColor(QPalette.ColorRole.HighlightedText, QColor("#FFFFFF"))
    result.setColor(QPalette.ColorRole.ToolTipBase, QColor(str(c["text"])))
    result.setColor(QPalette.ColorRole.ToolTipText, QColor(str(c["card"])))
    result.setColor(QPalette.ColorRole.PlaceholderText, QColor(str(c["muted"])))
    result.setColor(QPalette.ColorRole.Link, QColor(str(c["primary"])))
    result.setColor(QPalette.ColorRole.Mid, QColor(str(c["border"])))
    result.setColor(QPalette.ColorRole.Dark, QColor(str(c["border_strong"])))
    for role in (
        QPalette.ColorRole.WindowText,
        QPalette.ColorRole.Text,
        QPalette.ColorRole.ButtonText,
    ):
        result.setColor(QPalette.ColorGroup.Disabled, role, QColor(str(c["muted"])))
    return result


def apply(application) -> None:
    application.setPalette(palette())
    application.setStyleSheet(stylesheet())


def install(application) -> None:
    """设置 Fusion、字体与主题，并跟随系统深浅色变化。"""
    application.setStyle("Fusion")
    application.setFont(font(13))
    apply(application)
    hints = application.styleHints()
    signal = getattr(hints, "colorSchemeChanged", None)
    if signal is not None:
        signal.connect(lambda _scheme: apply(application))


def repolish(widget: QWidget) -> None:
    style = widget.style()
    style.unpolish(widget)
    style.polish(widget)
    widget.update()


def mix(first: str, second: str, ratio: float) -> str:
    """在两个 #RRGGBB 之间插值；ratio=0 取 first。"""
    ratio = max(0.0, min(1.0, ratio))
    a = QColor(first)
    b = QColor(second)
    return QColor(
        round(a.red() + (b.red() - a.red()) * ratio),
        round(a.green() + (b.green() - a.green()) * ratio),
        round(a.blue() + (b.blue() - a.blue()) * ratio),
    ).name()


def stylesheet() -> str:
    c = colors()
    family_name = fonts.family()
    return f"""
    QWidget {{
        color: {c['text']};
        font-family: "{family_name}";
        font-size: 14px;
    }}
    QMainWindow, QDialog {{
        background: {c['bg']};
    }}
    QDialog#sheet {{
        background: {c['card']};
    }}
    QLabel {{
        background: transparent;
    }}
    QLabel[tone="text"] {{
        color: {c['text']};
    }}
    QLabel[tone="muted"] {{
        color: {c['muted']};
    }}
    QLabel[tone="primary"] {{
        color: {c['primary']};
    }}
    QLabel[tone="success"] {{
        color: {c['success']};
    }}
    QLabel[tone="warning"] {{
        color: {c['warning']};
    }}
    QLabel[tone="danger"] {{
        color: {c['danger']};
    }}
    QMenu {{
        background: {c['card']};
        border: 1px solid {c['border']};
        padding: 6px;
    }}
    QMenu::item {{
        color: {c['text']};
        padding: 6px 24px 6px 12px;
        border-radius: 5px;
    }}
    QMenu::item:selected {{
        background: {c['primary_soft']};
    }}
    QMenu::item:disabled {{
        color: {c['muted']};
    }}
    QMenu::separator {{
        height: 1px;
        background: {c['border']};
        margin: 4px 8px;
    }}
    QScrollArea {{
        background: transparent;
        border: none;
    }}
    QScrollArea > QWidget > QWidget {{
        background: transparent;
    }}
    QScrollBar:vertical {{
        background: transparent;
        width: 8px;
        margin: 2px 0 2px 0;
    }}
    QScrollBar::handle:vertical {{
        background: {c['border_strong']};
        border-radius: 3px;
        min-height: 32px;
    }}
    QScrollBar::handle:vertical:hover {{
        background: {c['muted']};
    }}
    QScrollBar::add-line:vertical, QScrollBar::sub-line:vertical {{
        height: 0;
    }}
    QScrollBar::add-page:vertical, QScrollBar::sub-page:vertical {{
        background: transparent;
    }}
    QToolTip {{
        background: {c['text']};
        color: {c['card']};
        border: none;
        padding: 6px 8px;
    }}
    """
