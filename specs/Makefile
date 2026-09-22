# Builds every specification, and one combined Overleaf package for all three.
DOCS = functional design algebra

.PHONY: all overleaf clean distclean $(DOCS)

all: $(DOCS)

$(DOCS):
	$(MAKE) -C $@

# One Overleaf project for all three documents (they share ../common/); see
# OVERLEAF.md. Per-document zips are not produced: a single project with three
# main documents to choose between is simpler to maintain than three projects
# each holding its own copy of the shared files.
overleaf:
	python3 tools/package-overleaf.py

clean:
	for d in $(DOCS); do $(MAKE) -C $$d clean; done

distclean:
	for d in $(DOCS); do $(MAKE) -C $$d distclean; done
	$(RM) -r dist
