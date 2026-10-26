"""Run a command once 127.0.0.1:3100-3102 are free (other agents share the
fixed e2e ports); start it again while it fails only because a port was
taken first. Usage: run_when_free.py LOG CWD -- CMD..."""
import socket
import subprocess
import sys
import time

log, cwd = sys.argv[1], sys.argv[2]
cmd = sys.argv[sys.argv.index("--") + 1:]

def busy(port):
    s = socket.socket()
    s.settimeout(0.5)
    try:
        s.connect(("127.0.0.1", port))
        return True
    except OSError:
        return False
    finally:
        s.close()

for attempt in range(60):
    while any(busy(p) for p in (3100, 3101, 3102)):
        time.sleep(3)
    with open(log, "w") as f:
        r = subprocess.run(cmd, cwd=cwd, stdout=f, stderr=subprocess.STDOUT)
    text = open(log).read()
    if "is already used" in text and r.returncode != 0:
        time.sleep(10)
        continue
    print(f"exit {r.returncode} (attempt {attempt + 1})")
    sys.exit(r.returncode)
print("gave up: the ports stayed taken")
sys.exit(3)
