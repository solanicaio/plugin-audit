import { h, React } from '../runtime';
import { sx } from '../styles';
import { BarChart } from '@mui/x-charts/BarChart';
import { Box, Chip, Paper, Typography, useTheme } from '@mui/material';
import type { Histogram, Severity } from '../types';

const DAY = 24 * 60 * 60;

function tickLabel(d: Date, step: number, multiDay: boolean): string {
  if (step >= DAY) return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  const time = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
  return multiDay ? `${d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} ${time}` : time;
}

// EventsChart shows the number of events over time, stacked by severity.
// Clicking a bar zooms into its time, and the legend filters by severity.
export function EventsChart(props: {
  histogram: Histogram | null;
  severities: Severity[];
  onToggleSeverity: (s: Severity) => void;
  onZoom: (since: string, until: string) => void;
}): any {
  const { histogram, severities, onToggleSeverity, onZoom } = props;
  const theme = useTheme();
  const buckets = histogram?.buckets ?? [];
  const step = histogram?.step ?? 0;
  const dataset = React.useMemo(
    () => buckets.map((b) => ({ t: new Date(b.start), normal: b.normal, warning: b.warning })),
    [histogram]
  );
  const totals = buckets.reduce(
    (acc, b) => ({ Normal: acc.Normal + b.normal, Warning: acc.Warning + b.warning }),
    { Normal: 0, Warning: 0 }
  );
  const multiDay =
    buckets.length > 1 &&
    new Date(buckets[buckets.length - 1].start).toDateString() !== new Date(buckets[0].start).toDateString();
  // About eight labels, whatever the number of bars.
  const every = Math.max(1, Math.ceil(buckets.length / 8));
  const colors: Record<Severity, string> = {
    Normal: theme.palette.primary.main,
    Warning: theme.palette.warning.main,
  };

  const legend = (['Normal', 'Warning'] as const).map((s) =>
    h(Chip, {
      key: s,
      size: 'small',
      label: `${s} ${totals[s].toLocaleString()}`,
      variant: severities.includes(s) ? 'filled' : 'outlined',
      onClick: () => onToggleSeverity(s),
      title: severities.includes(s) ? 'Show all severities' : `Show ${s} events only`,
      icon: h(Box, { sx: { width: 10, height: 10, borderRadius: '2px', bgcolor: colors[s], ml: '8px !important' } }),
    })
  );

  return h(
    Paper,
    { variant: 'outlined', sx: sx.chart },
    h(
      Box,
      { sx: { display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' } },
      h(Typography, { variant: 'subtitle2', sx: { fontWeight: 600, mr: 'auto' } }, 'Events over time'),
      ...legend
    ),
    buckets.length === 0
      ? h(Box, { sx: { ...sx.empty, p: 4 } }, 'No events in this time range.')
      : h(BarChart, {
          height: 180,
          dataset,
          margin: { top: 12, right: 8, bottom: 4, left: 4 },
          xAxis: [
            {
              scaleType: 'band',
              dataKey: 't',
              categoryGapRatio: 0.15,
              tickLabelInterval: (_: Date, i: number) => i % every === 0,
              valueFormatter: (d: Date, ctx: { location: string }) =>
                ctx.location === 'tick'
                  ? tickLabel(d, step, multiDay)
                  : `${d.toLocaleString()} – ${new Date(d.getTime() + step * 1000).toLocaleTimeString()}`,
            },
          ],
          yAxis: [{ tickMinStep: 1, width: 36 }],
          series: [
            { dataKey: 'normal', label: 'Normal', stack: 'severity', color: colors.Normal },
            { dataKey: 'warning', label: 'Warning', stack: 'severity', color: colors.Warning },
          ],
          grid: { horizontal: true },
          hideLegend: true,
          onAxisClick: (_: unknown, d: { dataIndex: number } | null) => {
            const b = d ? buckets[d.dataIndex] : undefined;
            if (!b || b.normal + b.warning === 0) return;
            const since = new Date(b.start);
            onZoom(since.toISOString(), new Date(since.getTime() + step * 1000 - 1).toISOString());
          },
          sx: { cursor: 'pointer' },
        })
  );
}
