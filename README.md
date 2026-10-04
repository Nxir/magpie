# PR #753: reproducible background-memory UI comparison

Evidence for https://github.com/yetone/magpie/pull/753.

The images use **simulated gateway API responses and the real Magpie UI assets**
from two exact commits:

- Before: `7cc44b68a9afa0d92db689ff5ed0f5010a02b923` (PR base at creation).
- After: `0ae81b3c5a02a840d3d4983c9503cdbb1f86b291` (PR head).

The same six requests are rendered at both revisions: two independent unnamed
memory workers, plus a named chat containing an ordinary request and a memory
request. The fixture includes `memory_consolidation`, `memgen` and `memory`.
Session IDs, request counts, tokens and costs are identical in the comparison.
All displayed accounts and IDs are fictional.

The sandbox preview normally seeds ordinary requests. To see this change, it
needs Codex requests with a memory kind, a separate session ID and no chat title.
This harness supplies those responses directly; no Codex account, memory worker,
backend, model call or local user configuration is required.

## Comparisons

| Browser | Chinese | English |
| --- | --- | --- |
| Chromium | [Before / after](screenshots/chromium-zh-comparison.png) | [Before / after](screenshots/chromium-en-comparison.png) |
| WebKit | [Before / after](screenshots/webkit-zh-comparison.png) | [Before / after](screenshots/webkit-en-comparison.png) |

The individual screenshots are unmodified browser captures. The comparison
images put the before and after captures beside each other with commit labels.
Narrow-window captures are included separately.

## Validation

All **8 browser cases** passed (before/after × Chromium/WebKit × English/Chinese).
Each case checks the displayed labels, separate groups, the named mixed chat,
cost totals, a live request update, folded heading identity, the By request view
and a 560px layout. No page or console errors were recorded, and the mocked page
shows no error toast. See [results.json](screenshots/results.json) for the exact
fixture, captured headings/tooltips and assertion results.

After tooltip in Chinese (read from the real heading's `title` attribute):

> 后台记忆整理
>
> Codex 正在后台整理历史聊天中的记忆，回答结束后也可能继续运行。
>
> 会话 ID: 10000000-0000-4000-8000-000000000001

## Reproduce

Requirements: Git, Node.js and Playwright 1.62.1. The script reads assets with
`git show` and intercepts every browser request. On Linux, Playwright browser
dependencies may require `install --with-deps`.

```sh
git clone --branch codex/label-background-memory https://github.com/Nxir/magpie.git /tmp/magpie-pr-753
npm install --prefix /tmp/magpie-pr753-playwright --no-save --package-lock=false playwright@1.62.1
/tmp/magpie-pr753-playwright/node_modules/.bin/playwright install chromium webkit
NODE_PATH=/tmp/magpie-pr753-playwright/node_modules node reproduce.cjs --repo=/tmp/magpie-pr-753 --out=/tmp/pr753-evidence
```

Download [reproduce.cjs](reproduce.cjs) from this evidence branch before running
the final command. The pinned before/after revisions are reachable from the PR
branch. Arguments `--before=<revision>` and `--after=<revision>` can override
them. The default output directory is `screenshots/` beside the script.

These captures validate the UI response to the request metadata. They do not
exercise Codex's actual background memory scheduler or model execution.
