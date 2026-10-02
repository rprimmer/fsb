#!/bin/sh
# Writes common/version.tex: the version every specification's cover shows,
# and the commit of the code it describes (the last commit that changed
# anything outside specs/). Run it before building the specs for a release;
# the result is committed, so Overleaf, which has no Git, sees it too.
#
#   specs/tools/stamp-version.sh "1.0 (release candidate)"
set -eu
cd "$(dirname "$0")/.."
version=${1:?usage: stamp-version.sh VERSION}
commit=$(git log -1 --format=%h -- .. ':(exclude)specs')
date=$(git log -1 --format=%cd --date=format:'%B %-d, %Y' "$commit")
cat > common/version.tex <<TEX
% Written by tools/stamp-version.sh; do not edit by hand.
\newcommand{\fsbversion}{$version}
\newcommand{\fsbcommit}{$commit}
\newcommand{\fsbcodedate}{$date}
TEX
echo "common/version.tex: version $version, code at $commit ($date)"
