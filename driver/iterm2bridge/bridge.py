"""Bridge between the driver and iTerm2's Python API (task 0015).

Run as: python3 -c "<this file>" --session-id <iTerm2 session ID>

Protocol: one JSON object per line.

stdin (driver to bridge):
  {"op": "send_text", "text": "y\\n"}

stdout (bridge to driver):
  {"event": "ready"}                        bound to the session
  {"event": "error", "code": C, "message": M}   C is one of api_disabled,
                                            session_not_found,
                                            package_missing, other
  {"event": "prompt"}                       shell integration: prompt shown
  {"event": "command_start"}                a command began
  {"event": "command_end", "status": N}     a command finished, exit code N

Needs iTerm2 3.4 or later with Settings > General > Magic > Enable Python
API turned on, and `pip install iterm2`.
"""
import argparse
import asyncio
import json
import sys


def emit(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()


def fail(code, message):
    emit({"event": "error", "code": code, "message": message})
    return 1


async def pump_stdin(session):
    loop = asyncio.get_running_loop()
    while True:
        line = await loop.run_in_executor(None, sys.stdin.readline)
        if not line:
            return  # driver closed stdin: shut down
        try:
            cmd = json.loads(line)
        except ValueError:
            continue
        if cmd.get("op") == "send_text":
            await session.async_send_text(cmd.get("text", ""))


async def pump_prompts(iterm2, connection, session_id):
    mode_type = iterm2.PromptMonitor.Mode
    async with iterm2.PromptMonitor(connection, session_id) as monitor:
        while True:
            mode, info = await monitor.async_get()
            if mode == mode_type.PROMPT:
                emit({"event": "prompt"})
            elif mode == mode_type.COMMAND_START:
                emit({"event": "command_start"})
            elif mode == mode_type.COMMAND_END:
                emit({"event": "command_end", "status": int(info)})


async def run(session_id):
    try:
        import iterm2
    except ImportError:
        return fail("package_missing", "the iterm2 Python package is not installed")

    try:
        connection = await iterm2.Connection.async_create()
    except (FileNotFoundError, ConnectionRefusedError, PermissionError) as exc:
        # iTerm2 does not open its API socket while the toggle is off.
        return fail("api_disabled", str(exc))
    except Exception as exc:  # noqa: BLE001 - report anything else by name
        return fail("other", "%s: %s" % (type(exc).__name__, exc))

    app = await iterm2.async_get_app(connection)
    session = app.get_session_by_id(session_id)
    if session is None:
        return fail("session_not_found", "no iTerm2 session with ID " + session_id)

    emit({"event": "ready"})
    tasks = [
        asyncio.ensure_future(pump_stdin(session)),
        asyncio.ensure_future(pump_prompts(iterm2, connection, session_id)),
    ]
    done, pending = await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
    for task in pending:
        task.cancel()
    for task in done:
        exc = task.exception()
        if exc is not None:
            return fail("other", "%s: %s" % (type(exc).__name__, exc))
    return 0


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--session-id", required=True)
    args = parser.parse_args()
    sys.exit(asyncio.run(run(args.session_id)))


if __name__ == "__main__":
    main()
