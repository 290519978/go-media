"""
统一日志管理模块。
"""

import logging
import logging.handlers
import os
import sys


LOG_FILE = "analysis.log"


def _default_log_dir() -> str:
    script_dir = os.path.dirname(os.path.abspath(__file__))
    candidates = [
        os.path.join(script_dir, "configs", "logs", "ai"),
        os.path.join(script_dir, "..", "configs", "logs", "ai"),
    ]
    for path in candidates:
        parent = os.path.dirname(path)
        if os.path.exists(parent):
            return os.path.normpath(path)
    return os.path.normpath(candidates[-1])


def setup_logging(level_str: str = "INFO", retention_days: int = 3, log_dir: str = ""):
    level = getattr(logging, level_str.upper(), logging.INFO)
    normalized_log_dir = os.path.normpath(log_dir.strip()) if log_dir and log_dir.strip() else _default_log_dir()

    if not os.path.exists(normalized_log_dir):
        os.makedirs(normalized_log_dir, exist_ok=True)

    log_path = os.path.join(normalized_log_dir, LOG_FILE)

    root_logger = logging.getLogger()
    root_logger.setLevel(level)
    root_logger.handlers = []

    formatter = logging.Formatter(
        fmt="%(asctime)s | %(levelname)-8s | %(process)d:%(threadName)s | %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )

    console_handler = logging.StreamHandler(sys.stdout)
    console_handler.setFormatter(formatter)
    root_logger.addHandler(console_handler)

    file_handler = logging.handlers.TimedRotatingFileHandler(
        filename=log_path,
        when="midnight",
        interval=1,
        backupCount=retention_days,
        encoding="utf-8",
    )
    file_handler.suffix = "%Y-%m-%d.log"
    file_handler.setFormatter(formatter)
    root_logger.addHandler(file_handler)

    logging.info(
        "日志系统已初始化: level=%s path=%s retention_days=%s",
        level_str,
        log_path,
        retention_days,
    )
