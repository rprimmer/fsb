# Reduces the output of "callgraph -format={{.Caller}}<TAB>{{.Callee}}" to
# the calls between fsb's own functions and writes it as a Graphviz graph,
# one cluster per package. Closures are folded into their parent function.
# With --packages it writes one node per package instead, each edge labeled
# with the number of calls between the two packages.
import re, sys, collections
P = "github.com/rprimmer/fsb/"
def short(f):
    f = f.replace("(*", "(").replace(P, "")
    f = re.sub(r"\$\d+(\$\d+)*", "", f)        # closures -> parent function
    m = re.match(r"\((?:\*)?([\w/]+)\.(\w+)\)\.(\w+)", f)
    if m: return m.group(1), f"{m.group(2)}.{m.group(3)}"
    m = re.match(r"([\w/]+)\.([\w.]+)", f)
    return (m.group(1), m.group(2)) if m else (None, f)
edges = set()
for line in open(sys.argv[1]):
    a, _, b = line.rstrip("\n").partition("\t")
    if P not in a or P not in b: continue
    pa, fa = short(a); pb, fb = short(b)
    if (pa, fa) == (pb, fb) or fa.startswith("init") or fb.startswith("init"): continue
    edges.add(((pa, fa), (pb, fb)))
if "--packages" in sys.argv:
    calls = collections.Counter((x[0], y[0]) for x, y in edges if x[0] != y[0])
    funcs = collections.defaultdict(set)
    for x, y in edges: funcs[x[0]].add(x[1]); funcs[y[0]].add(y[1])
    print('digraph fsb {\n rankdir=LR; node [shape=box,style=rounded,fontname=Helvetica,fontsize=12]; edge [fontname=Helvetica,fontsize=10];')
    for pkg in sorted(funcs): print(f' "{pkg}" [label="{pkg}\\n{len(funcs[pkg])} function{"s" if len(funcs[pkg]) != 1 else ""}"];')
    for (a, b), n in sorted(calls.items()): print(f' "{a}" -> "{b}" [label="{n}",penwidth={1 + n / 8:.1f}];')
    print("}")
    sys.exit()
nodes = collections.defaultdict(set)
for x, y in edges: nodes[x[0]].add(x[1]); nodes[y[0]].add(y[1])
print("digraph fsb {\n rankdir=LR; node [shape=box,fontname=Helvetica,fontsize=10]; compound=true;")
for i, (pkg, fs) in enumerate(sorted(nodes.items())):
    print(f' subgraph cluster_{i} {{ label="{pkg}"; style=rounded; color=gray50;')
    for f in sorted(fs): print(f'  "{pkg}:{f}" [label="{f}"];')
    print(" }")
for (pa, fa), (pb, fb) in sorted(edges): print(f' "{pa}:{fa}" -> "{pb}:{fb}";')
print("}")
print(f"{len(edges)} edges, " + ", ".join(f"{p}:{len(f)}" for p, f in sorted(nodes.items())), file=sys.stderr)
