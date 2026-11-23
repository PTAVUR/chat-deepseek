"""调用 chat-deepseek 子进程的最小封装。"""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
import threading
from dataclasses import dataclass
from pathlib import Path
from typing import Callable, Sequence


@dataclass(frozen=True)
class CommandResult:
    args: tuple[str, ...]
    code: int
    stdout: str
    stderr: str

    @property
    def ok(self) -> bool:
        return self.code == 0


def binary_name() -> str:
    return "chat-deepseek.exe" if os.name == "nt" else "chat-deepseek"


def binary_in(directory: str | os.PathLike[str]) -> Path:
    return Path(directory).expanduser() / binary_name()


def find_binary() -> str:
    """优先找仓库根目录的 chat-deepseek，其次找 PATH。"""
    name = binary_name()
    if getattr(sys, "frozen", False):
        # PyInstaller 包内 __file__ 在临时解压目录；发布包里的 CLI 与 GUI 同级。
        # macOS 的 GUI 在 .app 内，CLI 仍在 .app 的上一层目录。
        executable = Path(sys.executable)
        directories = [executable.parent]
        if sys.platform == "darwin" and len(executable.parents) > 3:
            directories.append(executable.parents[3])
        for directory in directories:
            candidate = directory / name
            if candidate.is_file():
                return str(candidate)
    here = Path(__file__).resolve().parent
    for candidate in (here.parent / name, here.parent / "chat-deepseek"):
        if candidate.is_file():
            return str(candidate)
    found = shutil.which("chat-deepseek")
    return found or str(here.parent / name)


def run_command(
    binary: str, args: Sequence[str], stdin_text: str | None = None
) -> CommandResult:
    """执行一次短命令并收集输出；serve 不走这里。"""
    kwargs: dict[str, object] = {
        "text": True,
        "capture_output": True,
        "encoding": "utf-8",
        "errors": "replace",
    }
    if os.name == "nt":
        kwargs["creationflags"] = getattr(subprocess, "CREATE_NO_WINDOW", 0)
    if stdin_text is None:
        kwargs["stdin"] = subprocess.DEVNULL
    else:
        kwargs["input"] = stdin_text
    completed = subprocess.run([binary, *args], **kwargs)
    return CommandResult(
        args=tuple(args),
        code=completed.returncode,
        stdout=completed.stdout or "",
        stderr=completed.stderr or "",
    )


class ServerProcess:
    """持有 serve 子进程，把 stdout/stderr 按行回调给界面。"""

    def __init__(
        self,
        binary: str,
        args: Sequence[str],
        on_line: Callable[[str], None],
        on_exit: Callable[[int], None],
    ) -> None:
        self.binary = binary
        self.args = tuple(args)
        self.on_line = on_line
        self.on_exit = on_exit
        self.process: subprocess.Popen[str] | None = None
        self._thread: threading.Thread | None = None

    @property
    def running(self) -> bool:
        return self.process is not None and self.process.poll() is None

    def start(self) -> None:
        if self.running:
            return
        kwargs: dict[str, object] = {
            "stdout": subprocess.PIPE,
            "stderr": subprocess.STDOUT,
            "text": True,
            "encoding": "utf-8",
            "errors": "replace",
            "bufsize": 1,
            "start_new_session": os.name != "nt",
        }
        if os.name == "nt":
            kwargs["creationflags"] = getattr(subprocess, "CREATE_NO_WINDOW", 0)
        self.process = subprocess.Popen([self.binary, *self.args], **kwargs)
        self._thread = threading.Thread(target=self._pump, daemon=True)
        self._thread.start()

    def _pump(self) -> None:
        process = self.process
        if process is None or process.stdout is None:
            return
        try:
            for line in process.stdout:
                self.on_line(line.rstrip("\n"))
        finally:
            code = process.wait()
            self.on_exit(code)

    def stop(self, wait: bool = False, timeout: float = 10.0) -> None:
        process = self.process
        if process is None or process.poll() is not None:
            return
        try:
            process.terminate()
        except OSError:
            return
        if wait:
            try:
                process.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                process.kill()
