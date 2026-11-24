import json
from pathlib import Path
import sys

total = int(sys.argv[1])
if not 2 <= total <= 10000:
    raise SystemExit("expected 2 to 10000 active routing rules")
rules = []
for i in range(1, total):
    rule = {"type": "field", "port": str(2000 + i)}
    if i % 4 == 1:
        rule["balancerTag"] = "pool"
    else:
        rule.update(ruleTag=f"rule-{total-i:04}", outboundTag="direct")
    rules.append(rule)
config = {
    "log": {"loglevel": "none"},
    "outbounds": [{"protocol": "freedom", "tag": tag} for tag in ("direct", "pool-a", "pool-b")],
    "routing": {
        "balancers": [{"tag": "pool", "selector": ["pool-"], "strategy": {"type": "roundRobin"}}],
        "rules": rules,
    },
}
directory = Path(sys.argv[2])
(directory / "reference-xray.json").write_text(json.dumps(config))
# The reference inserts this list row for its management inbound. Give Node's
# otherwise identical fixture the same row so all returned pairs are equal.
rules.insert(0, {"type": "field", "inboundTag": ["API_INBOUND"], "outboundTag": "API"})
(directory / "xray.json").write_text(json.dumps(config))
