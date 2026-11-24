"""Build the benchmark scorecard, dashboard and charts from paired round CSVs."""

import csv
from collections import Counter, defaultdict
from pathlib import Path
from statistics import median


ROOT = Path(__file__).resolve().parent
XRAY = 'xray/results/'
TARGETS = ('papiliovpn', 'pasarguard')
BOTH = ('p50_us', 'p95_us')
OUTCOMES = ('PapilioVPN leads', 'Mixed', 'PasarGuard leads')
GROUPS = {
    'Host': 'host',
    'Instances': 'instances',
    'Runtime': 'runtime',
    'Traffic': 'traffic',
    'Online': 'online',
    'Routing': 'routing',
    'Accounts': 'accounts',
    'Observability': 'observability',
}


def cases_from_rounds():
    cases = {}

    def collect(group, filename, dimensions, operation=None, phase=None,
                include=lambda row: True, rate=None, shared=False, replace=False):
        source = filename if filename.startswith('host/') else XRAY + filename
        with (ROOT / source).open(newline='', encoding='utf-8') as file:
            rounds = list(csv.DictReader(file))
        grouped = defaultdict(lambda: defaultdict(dict))
        for row in rounds:
            if (phase is not None and row['phase'] != phase) or not include(row):
                continue
            if 'concurrency' in row:
                row['transport'], target, _ = row['case'].split('.')
                row['node'] = dict(node=TARGETS[0], pasar=TARGETS[1])[target]
                row['clients'] = row['concurrency']
            target = row.get('node', row.get('target'))
            if target not in TARGETS:
                raise ValueError(f'{source}: unexpected comparison target {target}')
            if int(row.get('rejected', 0)) or int(row.get('lost', 0)):
                raise ValueError(f'{source}: rejected requests or missing deliveries')
            if 'attempts' in row and int(row['attempts']) != int(row['success']):
                raise ValueError(f'{source}: failed synchronization')
            if row.get('workload') == 'route':
                row['rules'] = '0'
            dims = tuple((name, row[name]) for name in dimensions)
            key = (group, operation or row['operation'], row['transport'], dims)
            metrics = grouped[key][target]
            round_id = int(row['round'])
            if round_id in metrics:
                raise ValueError(f'{source}: duplicate round {key} {target} {round_id}')
            prefix = 'delivery_' if 'delivery_p50_us' in row else ''
            metrics[round_id] = tuple(float(row[prefix + name]) for name in BOTH)
            if rate:
                metrics[round_id] += (float(row[rate]),)
        if not grouped:
            raise ValueError(f'{source}: no workload matches phase {phase}')
        for key, targets in grouped.items():
            ours, reference = (targets[target] for target in TARGETS)
            if ours.keys() != reference.keys() or len(ours) < 3:
                raise ValueError(f'{source}: expected at least three paired rounds for {key}')
            if (key in cases) != replace:
                raise ValueError(f'{source}: unexpected overlap or missing base case {key}')
            a, b = (tuple(median(metric) for metric in zip(*result.values())) for result in (ours, reference))
            differences = [b[0] - a[0], b[1] - a[1]]
            if rate:
                differences.append(a[2] - b[2])
            outcome = OUTCOMES[1]
            if all(delta > 0 for delta in differences):
                outcome = OUTCOMES[0]
            if all(delta < 0 for delta in differences):
                outcome = OUTCOMES[2]
            cases[key] = (shared, outcome, source, phase or '', len(ours), a, b)

    def window(group, filename, dimensions, **options):
        collect(group, filename, dimensions, rate='goodput_per_sec', **options)

    collect('Host', 'host/results/2026-11-23-system-stats.csv', ('clients',), 'GetSystemStats',
            include=lambda row: row['case'].endswith('.system'), rate='polls_per_second', shared=True)
    collect('Instances', '2026-11-24-integrated-lifecycle.csv', (), phase='final',
            include=lambda row: row['operation'] != 'start')
    collect('Instances', '2026-11-24-integrated-lifecycle.csv', (), phase='final',
            include=lambda row: row['operation'] == 'start', shared=True)
    window('Runtime', '2026-11-23-integrated-control-retest.csv', ('clients',), operation='GetRuntimeStats',
           phase='before', include=lambda row: row['workload'] == 'runtime')
    for transport in ('http', 'grpc'):
        window('Traffic', f'2026-11-23-traffic-lean-matrix-{transport}.csv',
               ('scope', 'selection', 'accounts', 'clients'), operation='Read counters',
               include=lambda row: (row['scope'], row['selection']) != ('inbound', 'list'))
        window('Online', f'2026-11-23-online-{transport}.csv', ('scope', 'clients'), operation='Read IPs')
        window('Observability', f'2026-11-23-outbound-health-{transport}.csv',
               ('selection', 'clients'), operation='GetOutboundHealth')
    window('Traffic', '2026-11-23-traffic-reset-pair-bundled.csv', ('accounts', 'clients'),
           operation='Reset pair', phase='repeat', shared=True)
    window('Traffic', '2026-11-23-traffic-scope-reset-native-batch.csv', ('accounts', 'clients'),
           operation='Reset scope', include=lambda row: row['accounts'] == '1', shared=True)
    window('Traffic', '2026-11-24-compact-scope-reset.csv', ('accounts', 'clients'),
           operation='Reset scope', phase='final', shared=True)

    route_dimensions = ('rules', 'clients')
    window('Routing', '2026-11-23-routing-rounds.csv', route_dimensions, phase='initial')
    for phase in ('optimized', 'optimized-repeat'):
        window('Routing', '2026-11-23-routing-rounds.csv', route_dimensions, phase=phase, replace=True)
    for phase in ('after', 'after-long'):
        window('Routing', '2026-11-23-routing-buffer-tune.csv', route_dimensions, phase=phase, replace=True)
    window('Routing', '2026-11-23-routing-go1274-rounds.csv', route_dimensions, phase='patched')
    window('Routing', '2026-11-23-integrated-control-retest.csv', route_dimensions, operation='test',
           phase='after', include=lambda row: row['workload'] == 'route', replace=True)

    for transport, phase in (('http', 'final'), ('grpc', 'repeat-final')):
        collect('Accounts', '2026-11-24-integrated-sync-user.csv', ('case',), 'SyncUser', phase=phase,
                include=lambda row: row['transport'] == transport)
    collect('Observability', '2026-11-24-integrated-logs.csv', ('subscribers',), 'GetLogs', phase='final')
    return cases


def write_csv(cases):
    with (ROOT / 'scorecard.csv').open('w', newline='', encoding='utf-8') as file:
        writer = csv.writer(file, lineterminator='\n')
        writer.writerow(('group', 'operation', 'transport', 'workload', 'comparison', 'outcome',
                         'source', 'phase', 'rounds_per_target',
                         'node_p50_us', 'pasarguard_p50_us', 'node_p95_us', 'pasarguard_p95_us',
                         'node_success_per_sec', 'pasarguard_success_per_sec'))
        for (group, operation, transport, dims), (shared, outcome, source, phase, rounds, a, b) in cases.items():
            writer.writerow((group, operation, transport, ', '.join(f'{k}={v}' for k, v in dims),
                             'shared fixture' if shared else 'matched', outcome, source, phase, rounds,
                             a[0], b[0], a[1], b[1], *(a[2:] + b[2:] if len(a) > 2 else ('', ''))))


def counts(cases, group=None, shared=False):
    return Counter(outcome for key, (fixture, outcome, *_) in cases.items()
                   if fixture == shared and (group is None or group == key[0]))


def write_project_summaries(cases):
    matched = counts(cases)
    values = {
        'wins': matched[OUTCOMES[0]], 'mixed': matched[OUTCOMES[1]], 'losses': matched[OUTCOMES[2]],
        'matched': sum(matched.values()), 'shared': sum(counts(cases, shared=True).values()),
    }
    summaries = {
        'README.md': '**PapilioVPN led in {wins} of {matched} matched workloads** against PasarGuard 0.5.7. '
                     'Results were mixed in {mixed} workloads, and PasarGuard led in {losses}. '
                     'The dashboard also covers {shared} shared-fixture workloads with different inputs, output fields, or guarantees.',
        'README.fa.md': '**در {wins} از {matched} سناریوی هم‌ارز جلو بودیم**. '
                        'نتیجهٔ {mixed} سناریو ترکیبی بود و PasarGuard 0.5.7 در {losses} سناریو جلو بود. '
                        'گزارش کامل {shared} سناریوی دیگر با ورودی، خروجی یا تضمین‌های متفاوت را هم پوشش می‌دهد.',
        'README.ru.md': '**PapilioVPN лидирует в {wins} из {matched} сопоставимых сценариев**. '
                        'В {mixed} сценариях результаты смешанные, в {losses} лидирует PasarGuard 0.5.7. '
                        'Сводка также содержит {shared} сценариев на общей тестовой установке, '
                        'в которых различаются входные данные, поля ответа или гарантии.',
        'README.zh-CN.md': '**PapilioVPN 在 {matched} 个可比场景中的 {wins} 个领先**。'
                           '{mixed} 个场景的结果各有优劣，PasarGuard 0.5.7 在 {losses} 个场景领先。'
                           '汇总还列出 {shared} 个使用同一测试环境、但输入、输出字段或保证不同的场景。',
    }
    for filename, template in summaries.items():
        path = ROOT.parent / filename
        localized = values
        if filename == 'README.fa.md':
            digits = str.maketrans('0123456789', '۰۱۲۳۴۵۶۷۸۹')
            localized = {key: str(value).translate(digits) for key, value in values.items()}
        summary = template.format_map(localized)
        start, end = '<!-- benchmark-summary -->\n', '\n<!-- /benchmark-summary -->'
        before, marker, rest = path.read_text(encoding='utf-8').partition(start)
        _, closing, after = rest.partition(end)
        if not marker or not closing:
            raise ValueError(f'{filename}: missing benchmark summary markers')
        path.write_text(before + marker + summary + closing + after, encoding='utf-8')


def write_dashboard(cases):
    matched, shared = counts(cases), counts(cases, shared=True)
    total = sum(matched.values())
    lines = [
        '# Benchmarks', '',
        f'**PapilioVPN led in {matched[OUTCOMES[0]]} of {total} matched workloads** against PasarGuard 0.5.7 (`739eab9`). '
        f'Across {len(cases)} published comparisons, {sum(shared.values())} use a shared fixture with different public guarantees or response fields.', '',
        '![Matched workload outcomes by benchmark group](charts/outcomes.png)', '',
        '| Report | PapilioVPN leads | Mixed | PasarGuard leads | Shared fixture cases |',
        '| --- | ---: | ---: | ---: | ---: |',
    ]
    for group, filename in GROUPS.items():
        result = counts(cases, group)
        lines.append(f'| [{group}](reports/{filename}.md) | ' + ' | '.join(str(result[outcome]) for outcome in OUTCOMES)
                     + f' | {sum(counts(cases, group, True).values())} |')
    lines.extend((
        '| **Total** | ' + ' | '.join(f'**{matched[outcome]}**' for outcome in OUTCOMES)
        + f' | **{sum(shared.values())}** |', '',
        f'Shared fixture results are {shared[OUTCOMES[0]]} PapilioVPN leads, '
        f'{shared[OUTCOMES[1]]} mixed and {shared[OUTCOMES[2]]} PasarGuard leads. '
        'Host system stats has different fields and sampling behavior. Start uses a local config file for Node '
        'and sends the JSON in the request for PasarGuard. Traffic reset compares the same counters '
        'on unambiguous fixtures, with different selection and failure guarantees.', '',
        '![p95 ratio for selected paired workloads](charts/p95.png)', '',
        '## Reading the results', '',
        '- Each case is one operation, transport and workload. The scorecard records the specific CSV, phase '
        'and number of paired rounds for every case. A phase retained in the running code replaces an earlier '
        'measurement of the same workload where a selected repeat exists.',
        '- A lead requires lower median round p50 and p95 and, for windowed request tests, higher successful '
        'requests per second. Mixed covers split results and ties. No minimum margin or statistical confidence '
        'is inferred. Log comparisons score fresh delivery latency at a fixed producer rate.',
        '- Native-only diagnostics, traffic lists with extra management counters, and full/chunked sync '
        'with different restart or stream completion behavior are available in the group reports.',
        '- Published comparisons span several Node revisions and pinned fixtures. The linked reports give '
        'the exact environment and reproduction commands.', '',
        '[Scorecard CSV](scorecard.csv) · [Raw Xray results](xray/results/) · [Raw host results](host/results/)', '',
        '<a name="reproduce"></a><a name="xray-instance-lifecycle"></a>', '',
        '[Runner setup and lifecycle reproduction](reports/instances.md#reproduce)', '',
        '<a name="xray-runtime-statistics-after-the-rewrite"></a>', '',
        '[Runtime statistics and per-round data](reports/runtime.md#xray-runtime-statistics-after-the-rewrite)', '',
        'Regenerate the scorecard, charts, and README summaries from the repository root with Python 3 and Matplotlib.', '',
        '```sh', 'python3 benchmarks/summarize.py', '```', '',
    ))
    (ROOT / 'README.md').write_text('\n'.join(lines), encoding='utf-8')


def write_charts(cases):
    import matplotlib

    matplotlib.use('Agg')
    import matplotlib.pyplot as plt

    ink, muted, grid = '#16263a', '#50617a', '#e0e7ef'
    palette = ('#17795b', '#99701e', '#b4475b')
    plt.rcParams.update({'font.family': 'DejaVu Sans', 'font.size': 10, 'text.color': ink,
                         'axes.labelcolor': muted, 'xtick.color': muted, 'ytick.color': ink,
                         'axes.spines.top': False, 'axes.spines.right': False,
                         'axes.spines.left': False, 'axes.spines.bottom': False})
    groups = [group for group in GROUPS if counts(cases, group)]
    fig, ax = plt.subplots(figsize=(9, 4.0), layout='constrained')
    left = [0] * len(groups)
    for outcome, color in zip(OUTCOMES, palette):
        values = [counts(cases, group)[outcome] for group in groups]
        bars = ax.barh(groups, values, left=left, color=color, height=.6, label=outcome)
        ax.bar_label(bars, labels=[str(n) if n else '' for n in values], label_type='center', color='white', fontsize=10)
        left = [total + count for total, count in zip(left, values)]
    ax.invert_yaxis()
    ax.set_title(f'{sum(left)} matched performance workloads', loc='left', pad=14, weight='bold')
    ax.set_xlabel('Workloads')
    ax.set_axisbelow(True)
    ax.xaxis.grid(True, color=grid)
    ax.tick_params(length=0)
    ax.legend(loc='upper right', frameon=False, fontsize=9)
    fig.savefig(ROOT / 'charts/outcomes.png', dpi=165, facecolor='white')
    plt.close(fig)

    requested = (
        ('Observatory health · gRPC · 8 clients', 'GetOutboundHealth', 'grpc', {'selection': 'all', 'clients': '8'}),
        ('Traffic scope · gRPC · 1,000 counters', 'Reset scope', 'grpc', {'accounts': '500', 'clients': '1'}),
        ('Traffic scope · HTTP · 1,000 counters', 'Reset scope', 'http', {'accounts': '500', 'clients': '1'}),
        ('Live logs · gRPC · 1 subscriber', 'GetLogs', 'grpc', {'subscribers': '1'}),
    )
    fig, ax = plt.subplots(figsize=(9, 2.8), layout='constrained')
    for index, (label, operation, protocol, workload) in enumerate(requested):
        [(shared, _, _, _, _, a, b)] = [value for (_, op, api, dims), value in cases.items()
                                       if (op, api, dict(dims)) == (operation, protocol, workload)]
        ratio = a[1] / b[1]
        color = palette[0] if ratio < 1 else palette[2]
        ax.hlines(index, min(1, ratio), max(1, ratio), color=color, linewidth=2)
        ax.scatter(ratio, index, color=color, s=54, marker='D' if shared else 'o', zorder=3)
        ax.annotate(f'{ratio:.2f}×', (ratio, index), xytext=(9 if ratio > 1 else -9, 0),
                    textcoords='offset points', va='center', ha='left' if ratio > 1 else 'right')
    ax.axvline(1, color='#708196', linewidth=1)
    ax.set_yticks(range(len(requested)), [entry[0] for entry in requested])
    ax.invert_yaxis()
    ax.set(xscale='log', xlim=(.16, 4.8), xlabel='PapilioVPN / PasarGuard p95 · lower is faster')
    ax.set_xticks([.25, .5, 1, 2, 4], ['0.25×', '0.5×', '1×', '2×', '4×'])
    ax.tick_params(axis='both', which='both', length=0)
    ax.set_title('Where p95 differs', loc='left', pad=12, weight='bold')
    ax.text(.99, .98, '◆ Shared fixture', transform=ax.transAxes, ha='right', va='top', color=muted)
    fig.savefig(ROOT / 'charts/p95.png', dpi=165, facecolor='white')
    plt.close(fig)


if __name__ == '__main__':
    workload_cases = cases_from_rounds()
    write_csv(workload_cases)
    write_dashboard(workload_cases)
    write_charts(workload_cases)
    write_project_summaries(workload_cases)
