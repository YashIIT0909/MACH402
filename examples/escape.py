"""Negative test: everything here is expected to FAIL. The sandbox is the product.

A job that prints "BLOCKED" on every line has proved the isolation holds.
"""
import os, pathlib, socket, subprocess

def check(label, fn):
    try:
        print(f"ALLOWED  {label}: {fn()}")
    except Exception as exc:
        print(f"BLOCKED  {label}: {type(exc).__name__}: {exc}")

# 1. No network at all (NetworkMode: none) — this should not resolve, let alone connect.
check("outbound tcp", lambda: socket.create_connection(("1.1.1.1", 53), timeout=4))
check("dns", lambda: socket.gethostbyname("example.com"))

# 2. Read-only root filesystem — only /work, /out and /tmp are writable.
check("write /etc", lambda: pathlib.Path("/etc/cleargate").write_text("x"))
check("write /usr/lib", lambda: pathlib.Path("/usr/lib/cleargate").write_text("x"))

# 3. Capabilities dropped, no-new-privileges — no mounting, no raw sockets.
check("mount", lambda: subprocess.run(["mount", "-t", "proc", "proc", "/mnt"], capture_output=True, check=True))
check("raw socket", lambda: socket.socket(socket.AF_INET, socket.SOCK_RAW, socket.IPPROTO_ICMP))

# 4. The host's Docker socket must not be reachable from inside a job.
check("docker socket", lambda: pathlib.Path("/var/run/docker.sock").stat())

# These two are expected to be ALLOWED — they are the job's own workspace.
check("write /work", lambda: pathlib.Path("/work/scratch").write_text("x"))
check("write /out", lambda: pathlib.Path("/out/proof.txt").write_text("sandbox report\n"))

print(f"\nuid={os.getuid()} pid1={os.getpid()}")
