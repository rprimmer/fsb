# Builds every specification (and their Overleaf packages).
DOCS = functional design algebra

.PHONY: all overleaf clean distclean $(DOCS)

all: $(DOCS)

$(DOCS):
	$(MAKE) -C $@

overleaf:
	for d in $(DOCS); do $(MAKE) -C $$d overleaf; done

clean:
	for d in $(DOCS); do $(MAKE) -C $$d clean; done

distclean:
	for d in $(DOCS); do $(MAKE) -C $$d distclean; done
