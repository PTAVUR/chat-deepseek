from __future__ import annotations

import os
import sys
import unittest
from pathlib import Path
from unittest import mock

from PySide6.QtCore import Qt
from PySide6.QtGui import QColor, QPalette

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import theme


class _Hints:
    def __init__(self, scheme) -> None:
        self._scheme = scheme

    def colorScheme(self):  # noqa: N802 - Qt 命名
        return self._scheme


class _Application:
    def __init__(self, scheme, window: str) -> None:
        self._hints = _Hints(scheme)
        self._palette = QPalette()
        self._palette.setColor(
            QPalette.ColorRole.Window, QColor(window)
        )

    def styleHints(self):  # noqa: N802 - Qt 命名
        return self._hints

    def palette(self) -> QPalette:
        return self._palette


class SystemDarkTest(unittest.TestCase):
    def setUp(self) -> None:
        desktop = mock.patch.object(theme, "_desktop_dark", return_value=None)
        desktop.start()
        self.addCleanup(desktop.stop)

    def test_dark_scheme(self) -> None:
        application = _Application(Qt.ColorScheme.Dark, "#fafafa")
        self.assertTrue(theme.system_dark(application))

    def test_light_scheme_and_palette(self) -> None:
        application = _Application(Qt.ColorScheme.Light, "#fafafa")
        self.assertFalse(theme.system_dark(application))

    def test_unknown_scheme_uses_palette(self) -> None:
        dark = _Application(Qt.ColorScheme.Unknown, "#202020")
        light = _Application(Qt.ColorScheme.Unknown, "#fafafa")
        self.assertTrue(theme.system_dark(dark))
        self.assertFalse(theme.system_dark(light))

    def test_stale_light_scheme_does_not_hide_dark_palette(self) -> None:
        # 有些 Linux 桌面在启动瞬间会把 colorScheme 报成 Light，但平台调色板
        # 已经是暗色；Fusion 覆盖之前必须以调色板为准。
        application = _Application(Qt.ColorScheme.Light, "#202020")
        self.assertTrue(theme.system_dark(application))

    def test_desktop_dark_overrides_light_qt(self) -> None:
        # GNOME 直接启动时 Qt 常报 Light、调色板也是浅色，只能靠 gsettings。
        application = _Application(Qt.ColorScheme.Light, "#fafafa")
        with mock.patch.object(theme, "_desktop_dark", return_value=True):
            self.assertTrue(theme.system_dark(application))
        with mock.patch.object(theme, "_desktop_dark", return_value=False):
            self.assertFalse(theme.system_dark(application))


@unittest.skipUnless(sys.platform.startswith("linux"), "Linux 桌面检测")
class DesktopDarkTest(unittest.TestCase):
    def _env(self, **values: str):
        base = {
            "XDG_CURRENT_DESKTOP": "GNOME",
            "DESKTOP_SESSION": "",
            "GDMSESSION": "",
            "GTK_THEME": "",
        }
        base.update(values)
        return mock.patch.dict(os.environ, base, clear=False)

    def test_gtk_theme_env_wins(self) -> None:
        with self._env(GTK_THEME="Adwaita:dark"):
            self.assertTrue(theme._desktop_dark())

    def test_non_gnome_is_ignored(self) -> None:
        with self._env(XDG_CURRENT_DESKTOP="KDE"):
            self.assertIsNone(theme._desktop_dark())

    def test_prefer_dark(self) -> None:
        with (
            self._env(),
            mock.patch.object(
                theme,
                "_gsettings",
                side_effect=lambda _schema, key: (
                    "'prefer-dark'" if key == "color-scheme" else ""
                ),
            ),
        ):
            self.assertTrue(theme._desktop_dark())

    def test_prefer_light(self) -> None:
        with (
            self._env(),
            mock.patch.object(
                theme,
                "_gsettings",
                side_effect=lambda _schema, key: (
                    "'prefer-light'" if key == "color-scheme" else ""
                ),
            ),
        ):
            self.assertFalse(theme._desktop_dark())

    def test_default_scheme_uses_gtk_theme(self) -> None:
        with (
            self._env(),
            mock.patch.object(
                theme,
                "_gsettings",
                side_effect=lambda _schema, key: (
                    "'default'" if key == "color-scheme" else "'Yaru-dark'"
                ),
            ),
        ):
            self.assertTrue(theme._desktop_dark())

    def test_missing_gsettings_falls_back(self) -> None:
        with (
            self._env(),
            mock.patch.object(theme, "_gsettings", return_value=""),
        ):
            self.assertIsNone(theme._desktop_dark())


class SchemeSignalTest(unittest.TestCase):
    def setUp(self) -> None:
        self._hint = theme._dark_hint
        self._startup = theme._startup_done

    def tearDown(self) -> None:
        theme._dark_hint = self._hint
        theme._startup_done = self._startup

    def test_startup_light_signal_does_not_undo_dark_palette(self) -> None:
        theme._dark_hint = True
        theme._startup_done = False
        with mock.patch.object(theme, "apply") as apply:
            theme._follow_scheme(None, Qt.ColorScheme.Light)
            apply.assert_not_called()
        self.assertTrue(theme._dark_hint)

    def test_light_signal_after_startup_is_applied(self) -> None:
        theme._dark_hint = True
        theme._startup_done = True
        with mock.patch.object(theme, "apply") as apply:
            theme._follow_scheme(None, Qt.ColorScheme.Light)
            apply.assert_called_once()
        self.assertFalse(theme._dark_hint)


if __name__ == "__main__":
    unittest.main()
