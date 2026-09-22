#!/usr/bin/env python3
"""Create one source-only Overleaf ZIP for all three specifications.

usage: package-overleaf.py   (run from the specs folder)

The three documents (functional, design, algebra) share ../common/. Rather than
copy that folder into a separate ZIP per document (and rewrite every include to
match, which then has to be kept in sync with the real sources by hand), this
packages the whole specs/ source tree as one Overleaf project: common/,
functional/, design/ and algebra/ as siblings, exactly as they are in the repo.
No path is rewritten, so nothing here can drift from the checked-in sources.

Overleaf is a single-document editor: opening the project shows the file tree,
and you pick which .tex file is the project's "main document" (Menu > "Set
Main Document"). This one project therefore holds all three papers, and
switching which one compiles is a menu click, not a re-upload.

Excluded: build/ and dist/ (Overleaf builds its own output) and the committed
PDFs (Overleaf regenerates them; keeping a stale one out avoids confusion
about which is current).
"""
from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile

SPECS = Path(__file__).resolve().parents[1]
DOCS = ("functional", "design", "algebra")

files = [SPECS / "OVERLEAF.md"]
for doc in DOCS:
    d = SPECS / doc
    files += [d / f"fsb-{doc}.tex", d / "preamble.tex"]
    if (d / "macros.tex").exists():
        files.append(d / "macros.tex")
    files += sorted((d / "sections").glob("*.tex"))
files += sorted((SPECS / "common").glob("*.tex"))

dest = SPECS / "dist" / "fsb-specs-overleaf.zip"
dest.parent.mkdir(exist_ok=True)
with ZipFile(dest, "w", ZIP_DEFLATED) as z:
    for f in files:
        z.write(f, f.relative_to(SPECS))
print(dest)
