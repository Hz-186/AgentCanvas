from __future__ import annotations

import logging
import signal
import time

from agentcanvas_bridge.server import config_from_env, serve


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
    host, port, token, limits = config_from_env()
    server = serve(host, port, token, limits)
    stop = False

    def request_stop(*_args) -> None:
        nonlocal stop
        stop = True

    signal.signal(signal.SIGINT, request_stop)
    signal.signal(signal.SIGTERM, request_stop)
    while not stop:
        time.sleep(0.25)
    server.stop(grace=5).wait()


if __name__ == "__main__":
    main()
