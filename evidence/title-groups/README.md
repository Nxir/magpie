Evidence for PR #768, code commit 21f25b7720bf3f73b6d97b3d9efb2a1706cb5935.

Screenshots use the real Routing page assets with synthetic gateway/API results
from mocked upstreams. They demonstrate regrouping after a title write, without
another trace event. They are local replay, not production Codex captures.
The exported fingerprints use a fixed synthetic test key in isolated config
folders; no user prompt, credential, or installation private key is included.
benchmarks.txt contains local Apple M1 Pro microbenchmarks for this revision.
