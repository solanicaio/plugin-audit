import { h } from '../runtime';
import { TIME_RANGES } from '../filters';
import { Box, TextField, ToggleButton, ToggleButtonGroup } from '@mui/material';
import type { FilterState } from '../types';

const pad = (n: number) => String(n).padStart(2, '0');

// The datetime-local value of an ISO time, in local time.
function toInput(iso: string): string {
  const d = new Date(iso);
  if (!iso || Number.isNaN(d.getTime())) return '';
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function fromInput(v: string): string {
  const d = new Date(v);
  return v && !Number.isNaN(d.getTime()) ? d.toISOString() : '';
}

export function TimeRange(props: { filter: FilterState; onChange: (f: FilterState) => void }): any {
  const { filter, onChange } = props;
  return h(
    Box,
    { sx: { display: 'flex', gap: 1.5, alignItems: 'center', flexWrap: 'wrap' } },
    h(
      ToggleButtonGroup,
      {
        size: 'small',
        exclusive: true,
        value: filter.range,
        'aria-label': 'Time range',
        onChange: (_: unknown, range: string | null) => {
          if (!range) return;
          if (range === 'custom') {
            // Start from the range shown.
            const ms = TIME_RANGES.find((r) => r.key === filter.range)?.ms;
            onChange({
              ...filter,
              range,
              since: filter.since || (ms ? new Date(Date.now() - ms).toISOString() : ''),
            });
          } else {
            onChange({ ...filter, range, since: '', until: '' });
          }
        },
      },
      ...TIME_RANGES.map((r) => h(ToggleButton, { key: r.key, value: r.key, sx: { px: 1.5 } }, r.label)),
      h(ToggleButton, { key: 'custom', value: 'custom', sx: { px: 1.5 } }, 'Custom')
    ),
    filter.range === 'custom'
      ? (['since', 'until'] as const).map((key) =>
          h(TextField, {
            key,
            size: 'small',
            type: 'datetime-local',
            label: key === 'since' ? 'From' : 'To',
            value: toInput(filter[key]),
            onChange: (e: any) => onChange({ ...filter, [key]: fromInput(e.target.value) }),
            slotProps: { inputLabel: { shrink: true } },
            sx: { width: 220 },
          })
        )
      : null
  );
}
