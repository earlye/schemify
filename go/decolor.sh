#!/usr/bin/env bash
if command -v ansifilter >/dev/null 2>&1; then
    printf 'ansifilter -B -k';
    printf '| sed "s/\(.*\)\\[color=#00cd00\\]\(.*\)/  \\1\\2/g;"';
    printf '| sed "s/\(.*\)\\[color=#cd0000\\]\(.*\)/--\\1\\2/g;"';
    printf '| sed "s#\\[/color\\]##g"';
  else
    echo "cat";
fi
