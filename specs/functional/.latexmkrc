# Shared by make, latexmk, and editors invoked from this folder.
$pdf_mode = 1;
$out_dir = 'build';
$pdflatex = 'pdflatex -interaction=nonstopmode -halt-on-error -file-line-error %O %S';
@default_files = ('fsb-functional.tex');
