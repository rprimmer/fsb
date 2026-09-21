#!/usr/bin/env python3
"""Create a source-only Overleaf ZIP for one specification.

usage: package-overleaf.py DOCDIR      (run from the specs folder or a document folder)

The documents share ../common/. Overleaf takes one project, so the shared files are
copied into the ZIP as common/ and the includes are rewritten from ../common/ to
common/. Build output and PDFs are not included; Overleaf builds its own.
"""
import re
import sys
from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile

SPECS = Path(__file__).resolve().parents[1]
doc = Path(sys.argv[1]).resolve()
name = doc.name                                   # functional, design or algebra
main = f"fsb-{name}.tex"
if not (doc / main).exists():
    sys.exit(f"{doc / main} not found")

def flatten(text: str) -> str:
    return text.replace("../common/", "common/")

files = [main, "preamble.tex"]
if (doc / "macros.tex").exists():
    files.append("macros.tex")
files += sorted(str(p.relative_to(doc)) for p in (doc / "sections").glob("*.tex"))

dest = doc / "dist" / f"fsb-{name}-overleaf.zip"
dest.parent.mkdir(exist_ok=True)
with ZipFile(dest, "w", ZIP_DEFLATED) as z:
    for rel in files:
        z.writestr(rel, flatten((doc / rel).read_text(encoding="utf-8")))
    for p in sorted((SPECS / "common").glob("*.tex")):
        z.writestr(f"common/{p.name}", flatten(p.read_text(encoding="utf-8")))
    guide = (SPECS / "OVERLEAF.md").read_text(encoding="utf-8").replace("MAINFILE", main).replace("DOCNAME", name)
    z.writestr("OVERLEAF.md", guide)
    # Overleaf manages its own output directory: do not copy the local .latexmkrc.
    z.writestr("latexmkrc", "$pdf_mode = 1;\n")
print(dest)
