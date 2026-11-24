import json
from pathlib import Path
import sys

names = int(sys.argv[1])
if not 1 <= names <= 10000:
    raise SystemExit("expected 1 to 10000 traffic accounts")
config = {
    "log": {"loglevel": "none"},
    "stats": {},
    "policy": {
        "levels": {"0": {"statsUserUplink": True, "statsUserDownlink": True}},
        "system": {
            "statsInboundUplink": True, "statsInboundDownlink": True,
            "statsOutboundUplink": True, "statsOutboundDownlink": True,
        },
    },
    "inbounds": [{
        "tag": "fixture", "listen": "127.0.0.1", "port": 24440, "protocol": "http",
        "settings": {"accounts": [{"user": f"account-{i:04}", "pass": "fixture"} for i in range(names)]},
    }],
    "outbounds": [{"tag": "direct", "protocol": "freedom"}],
}
if sys.argv[3] == "online" or sys.argv[3].startswith("online-"):
    config["policy"]["levels"]["0"]["statsUserOnline"] = True
directory = Path(sys.argv[2])
(directory / "xray.json").write_text(json.dumps(config))
config["inbounds"][0]["port"] = 24441
(directory / "reference-xray.json").write_text(json.dumps(config))
