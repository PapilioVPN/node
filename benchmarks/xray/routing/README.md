# Xray routing

[Benchmark dashboard](../README.md)

The scorecard covers rule lists, balancer reads, route evaluations, rule writes,
and balancer override changes. Rule-list fixtures verify ordering and membership,
including the counterpart to PasarGuard's private management rule.

| Coverage | Report |
| --- | --- |
| Rule lists at 4 and 512 rules, balancer reads, response encoding | [Reads](reads.md) |
| Route evaluation, override and clear | [Evaluation and balancer control](control.md) |
| Append, replace, present-tag removal, absent-tag removal | [Rule writes](rule-writes.md) |
| Integrated HTTP TestRoute repeat | [Integration measurements](../instances/README.md#xray-integration-performance-recheck) |
