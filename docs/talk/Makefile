# Builds the talk: the call graph from the code, then the Beamer slides.
# Needs: TeX Live (lualatex or pdflatex, beamer, moloch), Graphviz (dot),
# and golang.org/x/tools/cmd/callgraph (go install ...@latest).

CALLGRAPH ?= $(shell go env GOPATH)/bin/callgraph

all: fsb-talk.pdf

callgraph.tsv: $(wildcard ../../cmd/fsb/*.go ../../internal/*/*.go ../../web/*.go)
	cd ../.. && $(CALLGRAPH) -algo=vta -format='{{.Caller}}	{{.Callee}}' ./cmd/fsb > docs/talk/$@

callgraph.dot: callgraph.py callgraph.tsv
	python3 callgraph.py callgraph.tsv > $@

packages.dot: callgraph.py callgraph.tsv
	python3 callgraph.py callgraph.tsv --packages > $@

%.pdf: %.dot
	dot -Tpdf $< -o $@

fsb-talk.pdf: fsb-talk.tex callgraph.pdf packages.pdf
	latexmk -pdf -interaction=nonstopmode -halt-on-error fsb-talk.tex

clean:
	latexmk -C fsb-talk.tex
	rm -f callgraph.tsv

.PHONY: all clean
