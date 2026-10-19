#!/usr/bin/env python3
"""Check relative Markdown links and fenced code-block pairing, not factual correctness."""
import json,re
from pathlib import Path
from urllib.parse import unquote
ROOT=Path(__file__).resolve().parents[1]

def main():
    # Only repository docs; vendored/installed trees carry their own Markdown.
    skip={'.git','.venv','node_modules','.next','test-results','playwright-report'}
    # Agent worktrees under .claude/worktrees are other checkouts, not this tree's docs.
    def ours(parts):return not skip & set(parts) and parts[:2]!=('.claude','worktrees')
    count=0;files=sorted(p for p in ROOT.rglob('*.md') if ours(p.relative_to(ROOT).parts))
    for path in files:
        text=path.read_text(encoding='utf-8')
        assert sum(1 for l in text.splitlines() if l.lstrip().startswith('```'))%2==0,path
        for target in re.findall(r'(?<!!)\[[^\]\n]+\]\(([^)]+)\)',text):
            if '://' in target or target.startswith(('#','mailto:')):continue
            relative=unquote(target.split('#',1)[0])
            assert (path.parent/relative).resolve().exists(),f'{path.relative_to(ROOT)} -> {target}'
            count+=1
    report={'status':'PASS','markdown_files':len(files),'relative_links_checked':count}
    (ROOT/'checks/docs-check.json').write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps(report,indent=2))
if __name__=='__main__':main()
