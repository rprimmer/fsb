DOCUMENT = fsb-algebra
BUILD    = build
PDF      = $(BUILD)/$(DOCUMENT).pdf
SOURCES  = $(DOCUMENT).tex preamble.tex macros.tex $(wildcard sections/*.tex)
LATEXMK ?= latexmk

.PHONY: all clean distclean

all: $(PDF)

$(PDF): $(SOURCES) | $(BUILD)
	$(LATEXMK) -pdf -interaction=nonstopmode -halt-on-error \
		-file-line-error -outdir=$(BUILD) $(DOCUMENT).tex

$(BUILD):
	mkdir -p $@

clean:
	$(LATEXMK) -c -outdir=$(BUILD) $(DOCUMENT).tex

distclean:
	$(LATEXMK) -C -outdir=$(BUILD) $(DOCUMENT).tex
	$(RM) -r $(BUILD)
