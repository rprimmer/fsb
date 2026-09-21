# Overleaf source package

Upload `fsb-DOCNAME-overleaf.zip` as a new Overleaf project (New Project, then
Upload Project).
Set the main document to `MAINFILE` and the compiler to pdfLaTeX.
No shell escape, external conversion tools, or generated files are needed.

## Editing

- `MAINFILE`: the root document (title, date, and the list of sections).
- `sections/*.tex`: the text, one file per numbered section.
- `preamble.tex`: the title used in the header and PDF metadata; it includes
  `common/preamble.tex`.
- `common/preamble.tex`: packages, page layout, colors and listing style shared by
  every fsb specification.
- `common/macros.tex`: shared macros: `\code`, `\Tests`, and the requirement
  environment (see below).

## Requirements and cross-references

In the functional specification each requirement is written

    \begin{req}{RUL-4} ... \end{req}

which defines the label `req:RUL-4`. Refer to it with `\rid{RUL-4}` (a link in the
PDF). A reference to an identifier that does not exist shows as `??` and is
reported by LaTeX as an undefined reference, so a renamed requirement cannot go
unnoticed. Identifiers are stable: never reuse one. In the design specification,
`\fq{RUL-4}` names a requirement of the functional specification (plain text, since
it lives in another document).

## Local check

After extracting the ZIP, from its top folder:

```sh
latexmk -pdf -interaction=nonstopmode -halt-on-error MAINFILE
```

This ZIP uses a `latexmkrc` without a custom output directory, because Overleaf
manages build output. The full source tree keeps `build/` and shares `../common/`
between the three specifications. Run `make overleaf` in the specification's folder
there to regenerate this package.
