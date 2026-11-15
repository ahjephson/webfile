#!/usr/bin/env python3
"""Prepare isolated, fictional files for a screenshot of the real development app."""

import argparse
import json
import os
from pathlib import Path
import tempfile
import subprocess
import zipfile

ROOT = Path(__file__).resolve().parent.parent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--serve", action="store_true", help="Run the built demo with a private demo account mapping (requires bubblewrap)")
    args = parser.parse_args()
    output = ROOT / "dist"
    output.mkdir(exist_ok=True)
    demo = Path(tempfile.mkdtemp(prefix="site-demo-", dir=output))
    projects = demo / "Projects"
    backups = demo / "Backups"
    projects.mkdir()
    backups.mkdir()
    for folder in ("Documents", "Media", "Research", "Scripts"):
        (projects / folder).mkdir()
    (projects / "Documents" / "Getting started.md").write_text(
        "# Welcome to WebFile\n\nBrowse your files, preview documents and manage transfers.\n"
    )
    (projects / "Research" / "Field notes.txt").write_text("Fictional demonstration data.\n")
    (projects / "Scripts" / "backup.sh").write_text("#!/bin/sh\nprintf 'Demo backup script\\n'\n")
    (projects / "Project brief.md").write_text(
        "# A workspace for your files\n\n## This week's work\n\n"
        "- Organise project documents\n- Review the research notes\n- Back up completed work\n\n"
        "All files in this workspace are fictional demonstration data.\n"
    )
    (projects / "Equipment.csv").write_text("item,quantity,status\nCamera,2,Ready\nRecorder,1,Ready\nTripod,3,Checked\n")
    (projects / "Workspace.json").write_text(json.dumps({"project": "Field study", "status": "Ready"}, indent=2))
    (projects / "Release notes.txt").write_text("Demo project update\n\nDocuments organised and ready for review.\n")
    with zipfile.ZipFile(projects / "Project archive.zip", "w") as archive:
        archive.writestr("README.txt", "Fictional project archive for the WebFile screenshot.\n")
    (backups / "Previous projects").mkdir()
    (backups / "Backup checklist.md").write_text("# Backup checklist\n\nVerify the destination before starting.\n")

    config = json.loads((ROOT / "webfile.dev.json").read_text())
    config["listen"] = "127.0.0.1:8124"
    config["roots"] = [{"name": "Projects", "path": str(projects)}, {"name": "Backups", "path": str(backups)}]
    config["runtime"]["worker_dir"] = str(demo / "workers")
    config["runtime"]["data_dir"] = str(demo / "data")
    config["thumbnails"]["cache_dir"] = str(demo / "thumbnails")
    config["thumbnails"]["cache_socket"] = str(demo / "thumbnails.sock")
    path = demo / "config.json"
    path.write_text(json.dumps(config, indent=2))
    (output / "site-demo-config.path").write_text(str(path))
    print(path, flush=True)
    if args.serve:
        # Change account names only inside the demo's mount namespace. The real
        # UID/GID and filesystem permissions remain those of the invoking user.
        passwd = demo / "passwd"
        groups = demo / "group"
        passwd.write_text(f"root:x:0:0:root:/root:/bin/sh\ndemo:x:{os.getuid()}:{os.getgid()}:Demo:/tmp:/bin/sh\n")
        groups.write_text(f"root:x:0:\ndemo:x:{os.getgid()}:demo\n")
        subprocess.run([
            "bwrap", "--ro-bind", "/", "/", "--dev", "/dev", "--tmpfs", "/tmp", "--bind", str(demo), str(demo),
            "--ro-bind", str(passwd), "/etc/passwd", "--ro-bind", str(groups), "/etc/group",
            "--setenv", "HOME", "/tmp", "--setenv", "USER", "demo", "--setenv", "LOGNAME", "demo",
            "--die-with-parent", "--", str(output / "webfile-site-demo"), "serve", "-config", str(path)
        ], check=True)


if __name__ == "__main__":
    main()
