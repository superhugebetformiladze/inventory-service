"""Счётчик неудачных попыток входа — защита от подбора пароля.

Работает как журнал отметок: на каждую неудачную попытку в список кладётся время.
Отметки старше пяти минут выбрасываются, и если свежих накопилось десять — вход
временно запрещается. Успешный вход очищает журнал.

Отметки лежат в памяти процесса. Этого достаточно для одного сервера; если приложение
запустят в нескольких копиях, у каждой будет свой счёт — тогда понадобится общее
хранилище вроде Redis.
"""

import time
from collections import defaultdict
from threading import Lock

from .config import settings

_attempts: dict[str, list[float]] = defaultdict(list)
_lock = Lock()


def _prune(key: str, now: float) -> list[float]:
    window_start = now - settings.login_window_seconds
    fresh = [item for item in _attempts[key] if item > window_start]
    _attempts[key] = fresh
    return fresh


def is_blocked(key: str) -> bool:
    now = time.time()
    with _lock:
        return len(_prune(key, now)) >= settings.login_attempts


def register_failure(key: str) -> None:
    now = time.time()
    with _lock:
        _prune(key, now)
        _attempts[key].append(now)


def reset(key: str) -> None:
    with _lock:
        _attempts.pop(key, None)


def retry_after(key: str) -> int:
    now = time.time()
    with _lock:
        items = _prune(key, now)
    if not items:
        return 0
    return max(1, int(settings.login_window_seconds - (now - min(items))))
