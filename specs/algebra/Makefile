DOC = fsb-algebra
LATEXMK ?= latexmk

.PHONY: all pdf clean distclean help

all: pdf

# latexmk discovers dependencies (including ../common) and resolves references.
# The finished PDF is copied next to the sources and tracked in Git.
pdf:
	$(LATEXMK) $(DOC).tex
	cp build/$(DOC).pdf $(DOC).pdf

# Remove intermediates; keep the finished PDF.
clean:
	$(LATEXMK) -c $(DOC).tex

distclean:
	$(LATEXMK) -C $(DOC).tex

help:
	@echo 'make            Build and refresh $(DOC).pdf with pdfLaTeX'
	@echo 'make clean      Remove build intermediates; keep the PDF'
	@echo 'make distclean  Remove build outputs; keep the top-level PDF'
	@echo '(make overleaf lives one level up: see ../OVERLEAF.md)'
