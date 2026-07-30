import logging

import requests

import config as c

log = logging.getLogger("spoty_lls.alerting")


def notify_telegram(text: str) -> None:
    if not (c.TELEGRAM_BOT_TOKEN and c.TELEGRAM_CHAT_ID):
        return

    try:
        response = requests.post(
            f"https://api.telegram.org/bot{c.TELEGRAM_BOT_TOKEN}/sendMessage",
            data={
                "chat_id": c.TELEGRAM_CHAT_ID,
                "text": text,
                "disable_web_page_preview": "true",
            },
            timeout=10,
        )
        response.raise_for_status()
    except Exception as e:
        log.error("telegram notify failed: %s", e)
    else:
        log.debug("telegram notify sent")
