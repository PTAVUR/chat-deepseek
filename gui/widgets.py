"""自绘控件：卡片、按钮、输入框、状态、开关、账户表与提示条。

圆角、描边和阴影全部用 QPainter 画，避免 QSS 圆角在低分屏上出现的锯齿。
"""

from __future__ import annotations

from functools import lru_cache

from PySide6.QtCore import (
    QPointF,
    QEvent,
    Property,
    QEasingCurve,
    QPoint,
    QPropertyAnimation,
    QRectF,
    QSize,
    Qt,
    QTimer,
    Signal,
)
from PySide6.QtGui import (
    QColor,
    QFontMetrics,
    QPainter,
    QPainterPath,
    QPalette,
    QPen,
)
from PySide6.QtSvg import QSvgRenderer
from PySide6.QtWidgets import (
    QAbstractButton,
    QFrame,
    QGraphicsOpacityEffect,
    QHBoxLayout,
    QLabel,
    QLineEdit,
    QPlainTextEdit,
    QPushButton,
    QSizePolicy,
    QVBoxLayout,
    QWidget,
)

import parsing
import theme


def label(
    text: str,
    size: int = 13,
    weight: int = 400,
    color: str = "text",
    parent: QWidget | None = None,
) -> QLabel:
    widget = QLabel(text, parent)
    widget.setFont(theme.font(size, weight))
    widget.setProperty("tone", color)
    return widget


class ElidedLabel(QLabel):
    """超长路径用省略号收尾，完整内容放在 tooltip。"""

    def __init__(
        self,
        text: str = "",
        size: int = 12,
        weight: int = 400,
        color: str = "text",
        parent: QWidget | None = None,
    ) -> None:
        super().__init__(parent)
        self._full = ""
        self.setFont(theme.font(size, weight))
        self.setProperty("tone", color)
        self.setText(text)

    def setText(self, text: str) -> None:  # noqa: N802
        self._full = text
        self.setToolTip(text)
        self._refresh()

    def _refresh(self) -> None:
        metrics = QFontMetrics(self.font())
        width = max(0, self.width() - 2)
        super().setText(
            metrics.elidedText(self._full, Qt.TextElideMode.ElideMiddle, width)
        )

    def resizeEvent(self, event) -> None:  # noqa: N802
        super().resizeEvent(event)
        self._refresh()


class BrandLogo(QWidget):
    """蓝色圆角底 + 白色鲸鱼标志（Simple Icons，CC0-1.0）。"""

    SIZE = 46

    def __init__(self, svg_path, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self.setFixedSize(self.SIZE, self.SIZE)
        self._renderer = QSvgRenderer(str(svg_path), self)

    def paintEvent(self, event) -> None:  # noqa: N802
        c = theme.colors()
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)
        rect = QRectF(self.rect()).adjusted(0.5, 0.5, -0.5, -0.5)
        painter.setPen(Qt.PenStyle.NoPen)
        painter.setBrush(QColor(c["primary"]))
        painter.drawPath(_rounded_path(rect, 18))
        self._renderer.render(painter, QRectF(8, 8, 30, 30))
        painter.end()


class Accent(QWidget):
    def __init__(self, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self.setFixedSize(3, 18)

    def paintEvent(self, event) -> None:  # noqa: N802
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)
        painter.setPen(Qt.PenStyle.NoPen)
        painter.setBrush(QColor(theme.colors()["primary"]))
        painter.drawPath(_rounded_path(QRectF(self.rect()), 2))
        painter.end()


class Card(QFrame):
    """白色圆角卡片，带柔和投影和 1px 浅描边。"""

    RADIUS = 32

    def __init__(self, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self.setFrameShape(QFrame.Shape.NoFrame)
        self.setAttribute(Qt.WidgetAttribute.WA_StyledBackground, False)
        self.body = QVBoxLayout(self)
        self.body.setContentsMargins(20, 16, 20, 16)
        self.body.setSpacing(12)
        self._shadow = theme.shadow(self, blur=30, dy=8, alpha=70)

    def changeEvent(self, event) -> None:  # noqa: N802
        if event.type() in (
            QEvent.Type.PaletteChange,
            QEvent.Type.ApplicationPaletteChange,
        ):
            self._refresh_shadow()
        super().changeEvent(event)

    def _refresh_shadow(self) -> None:
        r, g, b, _ = theme.colors()["shadow"]  # type: ignore[misc]
        alpha = 150 if theme.is_dark() else 70
        self._shadow.setColor(QColor(r, g, b, alpha))

    def paintEvent(self, event) -> None:  # noqa: N802
        c = theme.colors()
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)
        rect = QRectF(self.rect()).adjusted(0.5, 0.5, -0.5, -0.5)
        painter.setBrush(QColor(c["card"]))
        painter.setPen(QPen(QColor(c["border"]), 1))
        painter.drawPath(_rounded_path(rect, self.RADIUS))
        painter.end()


class Panel(QFrame):
    """摘要条这类浅色圆角面板。"""

    def __init__(
        self,
        radius: int = 20,
        background: str = "card_alt",
        border: str | None = "border",
        parent: QWidget | None = None,
    ) -> None:
        super().__init__(parent)
        self.setFrameShape(QFrame.Shape.NoFrame)
        self.setAttribute(Qt.WidgetAttribute.WA_StyledBackground, False)
        self.radius = radius
        self.background = background
        self.border = border

    def paintEvent(self, event) -> None:  # noqa: N802
        c = theme.colors()
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)
        rect = QRectF(self.rect()).adjusted(0.5, 0.5, -0.5, -0.5)
        painter.setBrush(QColor(c[self.background]))
        if self.border is not None:
            painter.setPen(QPen(QColor(c[self.border]), 1))
        else:
            painter.setPen(Qt.PenStyle.NoPen)
        painter.drawPath(_rounded_path(rect, self.radius))
        painter.end()


class VerticalRule(QWidget):
    """细竖线，用于把次要操作与危险操作分开。"""

    def __init__(self, height: int = 18, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self.setFixedSize(1, height)

    def paintEvent(self, event) -> None:  # noqa: N802
        painter = QPainter(self)
        painter.fillRect(self.rect(), QColor(str(theme.colors()["border"])))
        painter.end()


class SectionHeading(QWidget):
    def __init__(self, text: str, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        row = QHBoxLayout(self)
        row.setContentsMargins(0, 0, 0, 0)
        row.setSpacing(8)
        row.addWidget(Accent())
        row.addWidget(label(text, 14, 600))


def _bezier(points: list[QPointF], t: float) -> QPointF:
    work = list(points)
    while len(work) > 1:
        work = [
            QPointF(
                (1 - t) * first.x() + t * second.x(),
                (1 - t) * first.y() + t * second.y(),
            )
            for first, second in zip(work, work[1:])
        ]
    return work[0]


@lru_cache(maxsize=8)
def _corner_curve(radius: float) -> tuple[QPointF, ...]:
    """零曲率端点的圆角曲线；两端沿边线切入，避免圆弧与直边的曲率突变。"""
    r = radius
    controls = [
        QPointF(0, r),
        QPointF(0, r * 0.30),
        QPointF(0, r * 0.10),
        QPointF(r * 0.10, 0),
        QPointF(r * 0.30, 0),
        QPointF(r, 0),
    ]
    steps = 20
    return tuple(_bezier(controls, index / steps) for index in range(steps + 1))


def _capsule_path(rect: QRectF) -> QPainterPath:
    """高度小于宽度时用真正的胶囊端，避免连续曲率在小控件上显得方。"""
    path = QPainterPath()
    radius = rect.height() / 2
    path.addRoundedRect(rect, radius, radius)
    return path


def _rounded_path(rect: QRectF, radius: float) -> QPainterPath:
    """连续曲率圆角矩形：直边 -> 零曲率圆角 -> 直边。"""
    r = min(radius, rect.width() / 2, rect.height() / 2)
    path = QPainterPath()
    if r <= 0:
        path.addRect(rect)
        return path
    left, top = rect.left(), rect.top()
    right, bottom = rect.right(), rect.bottom()
    curve = _corner_curve(r)

    path.moveTo(left + r, top)
    path.lineTo(right - r, top)
    for point in reversed(curve):
        path.lineTo(right - point.x(), top + point.y())
    path.lineTo(right, bottom - r)
    for point in curve:
        path.lineTo(right - point.x(), bottom - point.y())
    path.lineTo(left + r, bottom)
    for point in reversed(curve):
        path.lineTo(left + point.x(), bottom - point.y())
    path.lineTo(left, top + r)
    for point in curve:
        path.lineTo(left + point.x(), top + point.y())
    path.closeSubpath()
    return path


class Avatar(QWidget):
    """账户头像：首字母圆形底色。"""

    SIZE = 28

    def __init__(
        self,
        username: str,
        disabled: bool = False,
        size: int = SIZE,
        parent: QWidget | None = None,
    ) -> None:
        super().__init__(parent)
        self._initial = (username[:1] or "?").upper()
        self._disabled = disabled
        self._selected = False
        self.setFixedSize(size, size)
        self.setFont(theme.font(max(11, round(size * 0.42)), 600))

    def set_selected(self, selected: bool) -> None:
        if selected != self._selected:
            self._selected = selected
            self.update()

    def paintEvent(self, event) -> None:  # noqa: N802
        c = theme.colors()
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)
        if self._selected:
            background = c["primary"]
            foreground = "#FFFFFF"
        else:
            background = c["muted_soft"] if self._disabled else c["primary_soft"]
            foreground = c["muted"] if self._disabled else c["primary"]
        painter.setPen(Qt.PenStyle.NoPen)
        painter.setBrush(QColor(background))
        painter.drawEllipse(QRectF(self.rect()))
        painter.setPen(QColor(foreground))
        painter.setFont(self.font())
        painter.drawText(self.rect(), Qt.AlignmentFlag.AlignCenter, self._initial)
        painter.end()


class ActionButton(QPushButton):
    """按钮：浅描边、低对比度，按下时下沉 1px。"""

    def __init__(
        self,
        text: str,
        kind: str = "secondary",
        height: int = 36,
        small: bool = False,
        parent: QWidget | None = None,
    ) -> None:
        super().__init__(text, parent)
        self.kind = kind
        self.small = small
        self.setCursor(Qt.CursorShape.PointingHandCursor)
        self.setFixedHeight(height)
        self.setFont(theme.font(12 if small else 13, 600 if kind == "primary" else 500))
        self.setSizePolicy(QSizePolicy.Policy.Fixed, QSizePolicy.Policy.Fixed)
        self.setAttribute(Qt.WidgetAttribute.WA_Hover, True)
        self._keyboard_focus = False

    def focusInEvent(self, event) -> None:  # noqa: N802
        self._keyboard_focus = event.reason() in (
            Qt.FocusReason.TabFocusReason,
            Qt.FocusReason.BacktabFocusReason,
            Qt.FocusReason.ShortcutFocusReason,
        )
        self.update()
        super().focusInEvent(event)

    def focusOutEvent(self, event) -> None:  # noqa: N802
        self._keyboard_focus = False
        self.update()
        super().focusOutEvent(event)

    def sizeHint(self) -> QSize:  # noqa: N802
        metrics = QFontMetrics(self.font())
        padding = 24 if self.small else 34
        return QSize(metrics.horizontalAdvance(self.text()) + padding, self.height())

    def enterEvent(self, event) -> None:  # noqa: N802
        self.update()
        super().enterEvent(event)

    def leaveEvent(self, event) -> None:  # noqa: N802
        self.update()
        super().leaveEvent(event)

    def _palette(self) -> tuple[str, str | None, str]:
        c = theme.colors()
        hovered = self.underMouse()
        pressed = self.isDown()
        if not self.isEnabled():
            if self.kind == "ghost":
                return "transparent", None, c["muted"]
            return c["card_alt"], c["border"], c["muted"]
        if self.kind == "primary":
            fill = (
                c["primary_pressed"]
                if pressed
                else c["primary_hover"]
                if hovered
                else c["primary"]
            )
            return fill, theme.mix(c["primary"], "#FFFFFF", 0.28), "#FFFFFF"
        if self.kind == "danger":
            fill = c["danger_soft"] if hovered or pressed else c["card"]
            return fill, theme.mix(c["danger"], c["card"], 0.72), c["danger"]
        if self.kind == "ghost":
            fill = c["card_alt"] if hovered or pressed else "transparent"
            return fill, None, c["text"] if hovered else c["muted"]
        fill = c["muted_soft"] if pressed else c["card_alt"] if hovered else c["card"]
        return fill, c["border"], c["text"]

    def paintEvent(self, event) -> None:  # noqa: N802
        fill, border, text = self._palette()
        radius = 15 if self.small else 18
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)
        rect = QRectF(self.rect()).adjusted(0.5, 0.5, -0.5, -0.5)
        if fill != "transparent":
            painter.setBrush(QColor(fill))
        else:
            painter.setBrush(Qt.BrushStyle.NoBrush)
        if border is not None:
            painter.setPen(QPen(QColor(border), 1))
        else:
            painter.setPen(Qt.PenStyle.NoPen)
        painter.drawPath(_capsule_path(rect))

        if self.hasFocus() and self._keyboard_focus:
            if self.kind == "primary":
                ring = QColor("#FFFFFF")
                ring.setAlpha(110)
            elif self.kind == "danger":
                ring = QColor(theme.colors()["danger"])
                ring.setAlpha(110)
            else:
                ring = QColor(theme.colors()["primary"])
                ring.setAlpha(150)
            painter.setBrush(Qt.BrushStyle.NoBrush)
            painter.setPen(QPen(ring, 1.2))
            painter.drawPath(_capsule_path(rect.adjusted(2, 2, -2, -2)))

        painter.setPen(QColor(text))
        painter.setFont(self.font())
        offset = 1 if self.isDown() else 0
        painter.drawText(
            rect.adjusted(0, offset, 0, offset),
            Qt.AlignmentFlag.AlignCenter,
            self.text(),
        )
        painter.end()


def button(
    text: str,
    kind: str = "secondary",
    parent: QWidget | None = None,
    height: int = 36,
    small: bool = False,
) -> ActionButton:
    return ActionButton(text, kind, height=height, small=small, parent=parent)


class StatusPill(QWidget):
    """状态胶囊：圆点 + 文案，自绘保证边缘平滑。"""

    HEIGHT = 26

    def __init__(self, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self._text = "未运行"
        self._tone = "muted"
        self.setFixedHeight(self.HEIGHT)
        self.setSizePolicy(QSizePolicy.Policy.Fixed, QSizePolicy.Policy.Fixed)
        self.setFont(theme.font(11, 600))

    def _tones(self) -> tuple[str, str]:
        return {
            "muted": ("muted", "muted_soft"),
            "success": ("success", "success_soft"),
            "warning": ("warning", "warning_soft"),
            "danger": ("danger", "danger_soft"),
        }.get(self._tone, ("muted", "muted_soft"))

    def set_state(self, text: str, tone: str) -> None:
        self._text = text
        self._tone = tone
        self.updateGeometry()
        self.update()

    def sizeHint(self) -> QSize:  # noqa: N802
        metrics = QFontMetrics(self.font())
        return QSize(10 + 5 + 7 + metrics.horizontalAdvance(self._text) + 12, self.HEIGHT)

    def paintEvent(self, event) -> None:  # noqa: N802
        c = theme.colors()
        foreground, background = self._tones()
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)
        rect = QRectF(self.rect()).adjusted(0.5, 0.5, -0.5, -0.5)
        painter.setPen(Qt.PenStyle.NoPen)
        painter.setBrush(QColor(c[background]))
        painter.drawPath(_capsule_path(rect))
        painter.setBrush(QColor(c[foreground]))
        painter.drawEllipse(QPoint(12, self.height() // 2), 2.6, 2.6)
        painter.setPen(QColor(c[foreground]))
        painter.setFont(self.font())
        painter.drawText(
            QRectF(20, 0, self.width() - 26, self.height()),
            Qt.AlignmentFlag.AlignLeft | Qt.AlignmentFlag.AlignVCenter,
            self._text,
        )
        painter.end()


class Switch(QAbstractButton):
    """官方风格胶囊开关：滑块带投影，切换有短促缓动。"""

    WIDTH = 42
    HEIGHT = 24
    THUMB = 18

    def __init__(self, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self.setCheckable(True)
        self.setFixedSize(self.WIDTH, self.HEIGHT)
        self.setCursor(Qt.CursorShape.PointingHandCursor)
        self._offset = 0.0
        self._hovered = False
        self._animation = QPropertyAnimation(self, b"offset", self)
        self._animation.setDuration(150)
        self._animation.setEasingCurve(QEasingCurve.Type.OutCubic)
        self.toggled.connect(self._animate)

    def _get_offset(self) -> float:
        return self._offset

    def _set_offset(self, value: float) -> None:
        self._offset = value
        self.update()

    offset = Property(float, _get_offset, _set_offset)

    def setChecked(self, checked: bool) -> None:  # noqa: N802 - Qt 命名
        super().setChecked(checked)
        self._offset = 1.0 if checked else 0.0
        self.update()

    def _animate(self, checked: bool) -> None:
        self._animation.stop()
        self._animation.setStartValue(self._offset)
        self._animation.setEndValue(1.0 if checked else 0.0)
        self._animation.start()

    def enterEvent(self, event) -> None:  # noqa: N802
        self._hovered = True
        self.update()
        super().enterEvent(event)

    def leaveEvent(self, event) -> None:  # noqa: N802
        self._hovered = False
        self.update()
        super().leaveEvent(event)

    def sizeHint(self) -> QSize:  # noqa: N802
        return QSize(self.WIDTH, self.HEIGHT)

    def paintEvent(self, event) -> None:  # noqa: N802
        c = theme.colors()
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)

        if not self.isEnabled():
            track = QColor(c["muted_soft"])
        elif self.isChecked():
            track = QColor(c["primary_hover"] if self._hovered else c["primary"])
        else:
            track = QColor(c["switch_off_hover"] if self._hovered else c["switch_off"])

        painter.setPen(Qt.PenStyle.NoPen)
        painter.setBrush(track)
        painter.drawPath(_capsule_path(QRectF(0, 0, self.WIDTH, self.HEIGHT)))

        x = 3 + self._offset * (self.WIDTH - self.THUMB - 6)
        y = (self.HEIGHT - self.THUMB) / 2
        painter.setBrush(QColor(15, 23, 42, 38))
        painter.drawEllipse(QRectF(x, y + 1, self.THUMB, self.THUMB))
        painter.setBrush(QColor("#FFFFFF"))
        painter.setPen(QPen(QColor(15, 23, 42, 18), 1))
        painter.drawEllipse(QRectF(x, y, self.THUMB, self.THUMB))
        painter.end()


class SwitchRow(QWidget):
    """开关 + 可点击文字。"""

    def __init__(
        self,
        text: str,
        checked: bool = True,
        parent: QWidget | None = None,
    ) -> None:
        super().__init__(parent)
        row = QHBoxLayout(self)
        row.setContentsMargins(0, 0, 0, 0)
        row.setSpacing(8)
        self.switch = Switch(self)
        self.switch.setChecked(checked)
        self.text = label(text, 12, 400, "muted")
        self.text.setCursor(Qt.CursorShape.PointingHandCursor)
        self.text.mouseReleaseEvent = lambda _event: self.switch.toggle()  # type: ignore[method-assign]
        row.addWidget(self.text)
        row.addWidget(self.switch)

    def isChecked(self) -> bool:  # noqa: N802
        return self.switch.isChecked()


class GroupChip(QWidget):
    """账户组标签：Owner / Admin 用权限色，User 用中性色。"""

    HEIGHT = 19
    TONES = {
        "Owner": ("primary", "primary_soft"),
        "Admin": ("warning", "warning_soft"),
        "User": ("muted", "muted_soft"),
    }

    def __init__(self, text: str, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self._text = text
        self.setFont(theme.font(10, 600))
        self.setFixedHeight(self.HEIGHT)
        self.setSizePolicy(QSizePolicy.Policy.Fixed, QSizePolicy.Policy.Fixed)
        self.setAttribute(Qt.WidgetAttribute.WA_TransparentForMouseEvents, True)

    def sizeHint(self) -> QSize:  # noqa: N802
        metrics = QFontMetrics(self.font())
        return QSize(metrics.horizontalAdvance(self._text) + 15, self.HEIGHT)

    def paintEvent(self, event) -> None:  # noqa: N802
        c = theme.colors()
        foreground, background = self.TONES.get(self._text, ("muted", "muted_soft"))
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)
        rect = QRectF(self.rect()).adjusted(0.5, 0.5, -0.5, -0.5)
        painter.setPen(Qt.PenStyle.NoPen)
        painter.setBrush(QColor(str(c[background])))
        painter.drawPath(_capsule_path(rect))
        painter.setPen(QColor(str(c[foreground])))
        painter.setFont(self.font())
        painter.drawText(rect, Qt.AlignmentFlag.AlignCenter, self._text)
        painter.end()


class StatusBadge(QWidget):
    """状态点 + 文案：比彩色胶囊安静，但仍能一眼分辨。"""

    HEIGHT = 20

    def __init__(
        self,
        text: str = "",
        tone: str = "success",
        parent: QWidget | None = None,
    ) -> None:
        super().__init__(parent)
        self._text = text
        self._tone = tone
        self.setFont(theme.font(11, 500))
        self.setFixedHeight(self.HEIGHT)
        self.setSizePolicy(QSizePolicy.Policy.Fixed, QSizePolicy.Policy.Fixed)
        self.setAttribute(Qt.WidgetAttribute.WA_TransparentForMouseEvents, True)

    def set_state(self, text: str, tone: str) -> None:
        self._text = text
        self._tone = tone
        self.updateGeometry()
        self.update()

    def sizeHint(self) -> QSize:  # noqa: N802
        metrics = QFontMetrics(self.font())
        return QSize(13 + metrics.horizontalAdvance(self._text) + 2, self.HEIGHT)

    def paintEvent(self, event) -> None:  # noqa: N802
        c = theme.colors()
        accent = str(c[self._tone])
        text = str(c["danger"] if self._tone == "danger" else c["muted"])
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)
        painter.setPen(Qt.PenStyle.NoPen)
        painter.setBrush(QColor(accent))
        painter.drawEllipse(QPointF(4.5, self.height() / 2), 3.0, 3.0)
        painter.setPen(QColor(text))
        painter.setFont(self.font())
        painter.drawText(
            QRectF(13, 0, self.width() - 13, self.height()),
            Qt.AlignmentFlag.AlignLeft | Qt.AlignmentFlag.AlignVCenter,
            self._text,
        )
        painter.end()


class UserRow(QFrame):
    """成员行：头像、用户名与组标签、右侧状态点，选中为整行长胶囊。"""

    clicked = Signal(str)

    HEIGHT = 60
    ROLE_ORDER = {"Owner": 0, "Admin": 1, "User": 2}

    def __init__(self, user: parsing.User, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self.username = user.username
        self.setFixedHeight(self.HEIGHT)
        self.setCursor(Qt.CursorShape.PointingHandCursor)
        self.setAccessibleName(
            f"{user.username}，{user.groups_text or '无组'}，{user.status_text}"
        )
        self._selected = False
        self._hovered = False

        row = QHBoxLayout(self)
        row.setContentsMargins(24, 0, 24, 0)
        row.setSpacing(12)
        self._avatar = Avatar(user.username, user.disabled, 36, self)
        row.addWidget(self._avatar)

        identity = QWidget(self)
        column = QVBoxLayout(identity)
        column.setContentsMargins(0, 0, 0, 0)
        column.setSpacing(3)
        name = label(user.username, 14, 600, "muted" if user.disabled else "text")
        name.setSizePolicy(QSizePolicy.Policy.Ignored, QSizePolicy.Policy.Preferred)
        name.setToolTip(user.username)
        column.addWidget(name)

        chips = QWidget(identity)
        chips.setFixedHeight(GroupChip.HEIGHT)
        chip_row = QHBoxLayout(chips)
        chip_row.setContentsMargins(0, 0, 0, 0)
        chip_row.setSpacing(5)
        groups = sorted(
            user.groups, key=lambda group: self.ROLE_ORDER.get(group, 9)
        )
        for group in groups:
            chip_row.addWidget(GroupChip(group, chips))
        chip_row.addStretch(1)
        column.addWidget(chips)
        row.addWidget(identity, 1, Qt.AlignmentFlag.AlignVCenter)

        status = StatusBadge(
            user.status_text, "danger" if user.disabled else "success", self
        )
        row.addWidget(status, 0, Qt.AlignmentFlag.AlignVCenter)

        for child in (
            self._avatar,
            identity,
            name,
            chips,
            status,
            *chips.findChildren(QWidget),
        ):
            child.setAttribute(Qt.WidgetAttribute.WA_TransparentForMouseEvents, True)

    def set_selected(self, selected: bool) -> None:
        if selected != self._selected:
            self._selected = selected
            self._avatar.set_selected(selected)
            self.update()

    def enterEvent(self, event) -> None:  # noqa: N802
        self._hovered = True
        self.update()
        super().enterEvent(event)

    def leaveEvent(self, event) -> None:  # noqa: N802
        self._hovered = False
        self.update()
        super().leaveEvent(event)

    def mouseReleaseEvent(self, event) -> None:  # noqa: N802
        if event.button() == Qt.MouseButton.LeftButton:
            self.clicked.emit(self.username)
        super().mouseReleaseEvent(event)

    def paintEvent(self, event) -> None:  # noqa: N802
        c = theme.colors()
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)
        if self._selected or self._hovered:
            body = QRectF(self.rect()).adjusted(4, 0, -5, 0)
            painter.setPen(Qt.PenStyle.NoPen)
            painter.setBrush(
                QColor(str(c["selected"] if self._selected else c["card_alt"]))
            )
            painter.drawPath(_capsule_path(body))
            if self._selected:
                border = QColor(str(c["primary"]))
                border.setAlpha(62)
                painter.setPen(QPen(border, 1))
                painter.setBrush(Qt.BrushStyle.NoBrush)
                painter.drawPath(_capsule_path(body))
        else:
            painter.setPen(QPen(QColor(str(c["border"])), 1))
            painter.drawLine(24, self.height() - 1, self.width() - 25, self.height() - 1)
        painter.end()


class CheckBox(QAbstractButton):
    """18px 方形勾选框，选中时画对勾。"""

    def __init__(self, text: str, checked: bool = False, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self.setText(text)
        self.setCheckable(True)
        self.setChecked(checked)
        self.setCursor(Qt.CursorShape.PointingHandCursor)
        self.setFont(theme.font(12, 500))
        self.setFixedHeight(24)

    def sizeHint(self) -> QSize:  # noqa: N802
        width = 26 + QFontMetrics(self.font()).horizontalAdvance(self.text())
        return QSize(width, 24)

    def paintEvent(self, event) -> None:  # noqa: N802
        c = theme.colors()
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)
        box = QRectF(0.5, 3.5, 17, 17)
        if self.isChecked():
            painter.setPen(Qt.PenStyle.NoPen)
            painter.setBrush(QColor(c["primary"]))
            painter.drawPath(_rounded_path(box, 8))
            path = QPainterPath()
            path.moveTo(4.5, 12.0)
            path.lineTo(8.0, 15.0)
            path.lineTo(13.8, 8.4)
            painter.setPen(
                QPen(
                    QColor("#FFFFFF"),
                    2.0,
                    Qt.PenStyle.SolidLine,
                    Qt.PenCapStyle.RoundCap,
                    Qt.PenJoinStyle.RoundJoin,
                )
            )
            painter.drawPath(path)
        else:
            painter.setPen(QPen(QColor(c["border_strong"]), 1.2))
            painter.setBrush(QColor(c["card_alt"]))
            painter.drawPath(_rounded_path(box, 8))
        painter.setPen(QColor(c["text"]))
        painter.setFont(self.font())
        painter.drawText(
            QRectF(26, 0, self.width() - 26, self.height()),
            Qt.AlignmentFlag.AlignLeft | Qt.AlignmentFlag.AlignVCenter,
            self.text(),
        )
        painter.end()


class ChoiceRow(QAbstractButton):
    """单选行：整行可点，左侧圆点。用于选择组。"""

    def __init__(self, text: str, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self.setText(text)
        self.setCheckable(True)
        self.setCursor(Qt.CursorShape.PointingHandCursor)
        self.setFont(theme.font(13))
        self.setFixedHeight(42)
        self._hovered = False
        self.setSizePolicy(QSizePolicy.Policy.Expanding, QSizePolicy.Policy.Fixed)

    def enterEvent(self, event) -> None:  # noqa: N802
        self._hovered = True
        self.update()
        super().enterEvent(event)

    def leaveEvent(self, event) -> None:  # noqa: N802
        self._hovered = False
        self.update()
        super().leaveEvent(event)

    def paintEvent(self, event) -> None:  # noqa: N802
        c = theme.colors()
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)
        rect = QRectF(self.rect()).adjusted(1, 1, -1, -1)
        if self.isChecked():
            painter.setBrush(QColor(c["primary_soft"]))
            painter.setPen(QPen(QColor(c["primary"]), 1.2))
        elif self._hovered:
            painter.setBrush(QColor(c["card_alt"]))
            painter.setPen(QPen(QColor(c["border"]), 1))
        else:
            painter.setBrush(QColor(c["card"]))
            painter.setPen(QPen(QColor(c["border"]), 1))
        painter.drawPath(_rounded_path(rect, 20))

        center = QPoint(19, self.height() // 2)
        painter.setBrush(Qt.BrushStyle.NoBrush)
        painter.setPen(
            QPen(QColor(c["primary"] if self.isChecked() else c["border_strong"]), 1.6)
        )
        painter.drawEllipse(center, 7, 7)
        if self.isChecked():
            painter.setPen(Qt.PenStyle.NoPen)
            painter.setBrush(QColor(c["primary"]))
            painter.drawEllipse(center, 3.6, 3.6)

        painter.setPen(QColor(c["text"]))
        painter.setFont(self.font())
        painter.drawText(
            QRectF(36, 0, self.width() - 48, self.height()),
            Qt.AlignmentFlag.AlignLeft | Qt.AlignmentFlag.AlignVCenter,
            self.text(),
        )
        painter.end()


class Field(QFrame):
    """带圆角底座的输入框；QLineEdit 本身无边框，避免圆角锯齿。"""

    def __init__(
        self,
        placeholder: str = "",
        password: bool = False,
        parent: QWidget | None = None,
        height: int = 40,
    ) -> None:
        super().__init__(parent)
        self.setFixedHeight(height)
        self.setSizePolicy(QSizePolicy.Policy.Expanding, QSizePolicy.Policy.Fixed)
        self._focused = False
        self._focus_visible = False
        self.edit = QLineEdit(self)
        self.edit.setFrame(False)
        self.edit.setFont(theme.font(13))
        self.edit.setStyleSheet(
            "QLineEdit { background: transparent; border: none; padding: 0; }"
        )
        self.edit.setPlaceholderText(placeholder)
        if password:
            self.edit.setEchoMode(QLineEdit.EchoMode.Password)
        layout = QHBoxLayout(self)
        layout.setContentsMargins(12, 0, 12, 0)
        layout.setSpacing(0)
        layout.addWidget(self.edit)
        self.edit.installEventFilter(self)
        self._apply_palette()

    def _apply_palette(self) -> None:
        c = theme.colors()
        palette = self.edit.palette()
        palette.setColor(QPalette.ColorRole.Text, QColor(c["text"]))
        palette.setColor(QPalette.ColorRole.PlaceholderText, QColor(c["muted"]))
        palette.setColor(QPalette.ColorRole.Highlight, QColor(c["primary"]))
        palette.setColor(QPalette.ColorRole.HighlightedText, QColor("#FFFFFF"))
        self.edit.setPalette(palette)

    def changeEvent(self, event) -> None:  # noqa: N802
        if event.type() in (
            QEvent.Type.PaletteChange,
            QEvent.Type.ApplicationPaletteChange,
        ):
            self._apply_palette()
        super().changeEvent(event)

    def eventFilter(self, watched, event) -> bool:  # noqa: N802
        if watched is self.edit:
            if event.type() == QEvent.Type.FocusIn:
                self._focused = True
                self._focus_visible = event.reason() not in (
                    Qt.FocusReason.ActiveWindowFocusReason,
                    Qt.FocusReason.PopupFocusReason,
                    Qt.FocusReason.OtherFocusReason,
                )
                self.update()
            elif event.type() == QEvent.Type.FocusOut:
                self._focused = False
                self._focus_visible = False
                self.update()
        return super().eventFilter(watched, event)

    def paintEvent(self, event) -> None:  # noqa: N802
        c = theme.colors()
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)
        rect = QRectF(self.rect()).adjusted(0.5, 0.5, -0.5, -0.5)
        active = self._focused and self._focus_visible
        painter.setBrush(QColor(c["card"] if active else c["card_alt"]))
        painter.setPen(QPen(QColor(c["primary"] if active else c["border"]), 1))
        painter.drawPath(_rounded_path(rect, 20))
        painter.end()

    def text(self) -> str:
        return self.edit.text()

    def setText(self, value: str) -> None:  # noqa: N802
        self.edit.setText(value)

    def setPlaceholderText(self, value: str) -> None:  # noqa: N802
        self.edit.setPlaceholderText(value)

    def setEchoMode(self, mode) -> None:  # noqa: N802
        self.edit.setEchoMode(mode)

    def setFocus(self) -> None:  # noqa: N802
        self.edit.setFocus()

    @property
    def returnPressed(self):  # noqa: N802
        return self.edit.returnPressed


class TextBox(QFrame):
    """圆角只读输出框，内部 QPlainTextEdit 无边框。"""

    def __init__(self, parent: QWidget | None = None) -> None:
        super().__init__(parent)
        self.edit = QPlainTextEdit(self)
        self.edit.setFrameShape(QFrame.Shape.NoFrame)
        self.edit.setReadOnly(True)
        self.edit.setStyleSheet(
            "QPlainTextEdit { background: transparent; border: none; padding: 0; }"
        )
        layout = QVBoxLayout(self)
        layout.setContentsMargins(10, 8, 10, 8)
        layout.addWidget(self.edit)

    def paintEvent(self, event) -> None:  # noqa: N802
        c = theme.colors()
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)
        rect = QRectF(self.rect()).adjusted(0.5, 0.5, -0.5, -0.5)
        painter.setBrush(QColor(c["card_alt"]))
        painter.setPen(QPen(QColor(c["border"]), 1))
        painter.drawPath(_rounded_path(rect, 20))
        painter.end()

    def setPlainText(self, text: str) -> None:  # noqa: N802
        self.edit.setPlainText(text)


class Toast(QWidget):
    """窗口底部居中的短提示，带淡入淡出。"""

    def __init__(self, parent: QWidget) -> None:
        super().__init__(parent)
        self.setAttribute(Qt.WidgetAttribute.WA_TransparentForMouseEvents, True)
        self.setFont(theme.font(12, 600))
        self._text = ""
        self._error = False
        self.hide()
        self._opacity = QGraphicsOpacityEffect(self)
        self._opacity.setOpacity(0.0)
        self.setGraphicsEffect(self._opacity)
        self._fade = QPropertyAnimation(self._opacity, b"opacity", self)
        self._fade.setDuration(140)
        self._hide_timer = QTimer(self)
        self._hide_timer.setSingleShot(True)
        self._hide_timer.timeout.connect(self._fade_out)

    def show_message(self, message: str, error: bool = False) -> None:
        self._text = message
        self._error = error
        metrics = QFontMetrics(self.font())
        self.setFixedSize(metrics.horizontalAdvance(message) + 34, 36)
        self.reposition()
        self.show()
        self.raise_()
        self._fade.stop()
        self._fade.setStartValue(self._opacity.opacity())
        self._fade.setEndValue(1.0)
        self._fade.start()
        self._hide_timer.start(2600)
        self.update()

    def reposition(self) -> None:
        parent = self.parentWidget()
        if parent is None:
            return
        x = (parent.width() - self.width()) // 2
        y = parent.height() - self.height() - 22
        self.move(max(x, 12), max(y, 12))

    def paintEvent(self, event) -> None:  # noqa: N802
        c = theme.colors()
        foreground = c["danger"] if self._error else c["success"]
        background = c["danger_soft"] if self._error else c["success_soft"]
        painter = QPainter(self)
        painter.setRenderHint(QPainter.RenderHint.Antialiasing)
        rect = QRectF(self.rect()).adjusted(0.5, 0.5, -0.5, -0.5)
        painter.setBrush(QColor(background))
        painter.setPen(QPen(QColor(foreground), 1))
        painter.drawPath(_capsule_path(rect))
        painter.setPen(QColor(foreground))
        painter.setFont(self.font())
        painter.drawText(rect, Qt.AlignmentFlag.AlignCenter, self._text)
        painter.end()

    def _fade_out(self) -> None:
        self._fade.stop()
        self._fade.setStartValue(self._opacity.opacity())
        self._fade.setEndValue(0.0)
        self._fade.finished.connect(self._after_fade)
        self._fade.start()

    def _after_fade(self) -> None:
        try:
            self._fade.finished.disconnect(self._after_fade)
        except (RuntimeError, TypeError):
            pass
        if self._opacity.opacity() <= 0.01:
            self.hide()
