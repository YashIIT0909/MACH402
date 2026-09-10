"""The smallest possible paid job: prove the container ran and wrote an artifact."""
import pathlib, platform, sys

print(f"hello from {platform.python_version()} on {platform.machine()}")
print(f"argv: {sys.argv}")

# /out is the only place worth writing to: it is the volume the renter downloads.
pathlib.Path("/out/hello.txt").write_text("this file was produced by a paid ClearGate job\n")
print("wrote /out/hello.txt")
