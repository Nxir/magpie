# Codex image-title grouping evidence

Synthetic local reproduction of the Codex desktop image-message layout reported after PR #768. No user prompts, images, account identities, gateway logs, or private fingerprint keys are included.

The parser regression fails against the original implementation (one-image prompt has no matching fingerprint), and passes after recognizing the desktop attachment preamble and complete image wrappers. Unknown media, incomplete wrappers, additional prose outside the request field, empty questions, and multimodal title-helper templates remain unsupported.

`gateway-routes.json` is exported from `TestTitleLinkGatewayTransports/native_two_images`: actual HTTP requests through the gateway, including the Codex zstd transport, a synthetic image payload, and a fake local upstream. Parent IDs are absent at capture time. The routes contain synthetic keyed fingerprints, not prompt or image content.

`title-association.json` is exported from `TestAutomaticTitleGroupsAPI`, using those gateway records and an isolated Codex title index. It exercises a late title write, persisted history after restart, name refresh without rereading history, and conflicting recipients. Browser tests serve the real UI assets with these exported API results.

`*-title-after.png` shows grouping after the title is applied. `*-title-after-navigation.png` shows the same grouping after switching to the history day and reloading the page. The generated before screenshot is a pending-title-write state, not a screenshot of the old release.

These are local simulated integration results, not acceptance evidence from the installed production application.

Reproduce from the PR checkout (Go and Playwright must be available):

```sh
mkdir -p /tmp/title-image-evidence
TITLE_LINK_FIXTURE_FILE=/tmp/title-image-evidence/gateway-routes.json go test -tags nogui ./internal/gateway -run '^TestTitleLinkGatewayTransports/native_two_images$' -count=1
TITLE_LINK_FIXTURE_FILE=/tmp/title-image-evidence/gateway-routes.json ARTIFACT_DIR=/tmp/title-image-evidence go test -tags nogui ./internal/gui -run '^TestAutomaticTitleGroupsAPI$' -count=1
TITLE_ASSOCIATION_FIXTURE=/tmp/title-image-evidence/title-association.json ARTIFACT_DIR=/tmp/title-image-evidence node --test --test-concurrency=2 internal/gui/tests/routing-sessions.test.cjs
go test -tags nogui ./internal/gateway -run '^TestTitleImagePromptEvidence$' -bench '^BenchmarkTitleLinkRequest$' -benchtime=300ms
```

The Apple M1 Pro benchmark is in `benchmarks.txt`. A 5.8 MB image request took approximately 10.7 ms for initial evidence parsing; the existing cached-turn path took approximately 2 microseconds and does not reparse the request body. These figures are local measurements, not end-to-end model latency.

Production acceptance requires a new chat with an image and a nonempty text question on a build containing this fix. Earlier image requests have no recorded first-prompt fingerprint and are not backfilled.
