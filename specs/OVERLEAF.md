# Overleaf source package

`dist/fsb-specs-overleaf.zip` is one Overleaf project holding all three
specifications: the functional specification, the design specification, and
the algebraic specification. They share `common/preamble.tex` and
`common/macros.tex`, so this is packaged as a single project rather than three
separate ones — there is then exactly one copy of the shared files, and no
path is rewritten to make it work, so nothing here can drift from the sources
in this repository.

## Uploading

New Project → Upload Project → select the zip. Set the compiler to pdfLaTeX
(Overleaf's default). No shell escape, external conversion tools, or generated
files are needed.

## Compiling a different document

Overleaf compiles one "main document" at a time. The project starts on
whichever file was opened first; to compile a different specification, click
it in the file list on the left:

- `functional/fsb-functional.tex`
- `design/fsb-design.tex`
- `algebra/fsb-algebra.tex`

then open the project menu (the file icon in the top-left, or the ⚙ menu,
depending on your Overleaf version) and choose **Set as Main File**. This is a
per-project setting, not a rebuild: it takes effect on the next compile.

## Editing

- `<document>/fsb-<document>.tex`: that document's root file (title, date, and
  its list of sections).
- `<document>/sections/*.tex`: the text, one file per numbered section.
- `<document>/preamble.tex`: the title used in that document's header and PDF
  metadata; it includes `common/preamble.tex`.
- `common/preamble.tex`: packages, page layout, colors and listing style
  shared by all three documents.
- `common/macros.tex`: shared macros — `\code`, `\Tests`, and the requirement
  environment described below.

## Requirements and cross-references

In the functional specification, each requirement is written

    \begin{req}{RUL-4} ... \end{req}

which defines the label `req:RUL-4`. Refer to it **from within the functional
specification** with `\rid{RUL-4}` (a link in the PDF). A reference to an
identifier that does not exist shows as `??` and is reported by LaTeX as an
undefined reference, so a renamed requirement cannot go unnoticed. Identifiers
are stable: never reuse one.

`\rid{}` only resolves inside the document that defines the requirement. The
design specification refers to functional-specification requirements from
outside, so it uses `\fq{RUL-4}` instead — plain text, not a link, since it
names something in another document. Use `\fq{}`, not `\rid{}`, for a
cross-document reference; using `\rid{}` there compiles to an undefined
reference even though the requirement really exists (this happened once while
writing the design specification's traceability section, and was caught by
exactly this check).

## Bringing edits back

Overleaf edits stay in the Overleaf project until you bring them back. There
is no automatic sync (that needs Overleaf's paid Git integration, not set up
here). To bring a change back: download the edited `.tex` file(s) from
Overleaf and copy them over the matching path under this repository's
`specs/` folder, then from the repository root run

```sh
make -C specs
```

to refresh the committed PDFs, and commit both the `.tex` change and the
refreshed PDF together.

## Local check

To check the whole tree compiles without Overleaf, from this `specs/` folder:

```sh
make            # builds all three PDFs with latexmk
make overleaf   # regenerates dist/fsb-specs-overleaf.zip
```

Each document also builds on its own: `make -C functional`, `make -C design`,
`make -C algebra`.

## Version stamp

Each cover shows the version and the commit of the code it describes, from
`common/version.tex`. Overleaf has no Git, so that file is generated locally and
committed: run `specs/tools/stamp-version.sh "1.0"` (with the version) before
building the PDFs or the Overleaf package for a release.
