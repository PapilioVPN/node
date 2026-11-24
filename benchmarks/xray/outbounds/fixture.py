import json
import sys

outbounds = [{"tag": f"sample-{i:02}", "protocol": "freedom"} for i in range(16)]
outbounds[-1]["settings"] = {"redirect": "127.0.0.1:24443"}
outbounds.append({"tag": "unobserved", "protocol": "freedom"})
with open(sys.argv[1], "w") as output:
    json.dump({
        "log": {"loglevel": "none"},
        "observatory": {
            "subjectSelector": ["sample-"],
            "probeURL": "http://127.0.0.1:24442/health",
            "probeInterval": "1h",
            "enableConcurrency": True,
        },
        "outbounds": outbounds,
    }, output)
