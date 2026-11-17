import { h } from '../runtime';
import { LIST_FILTERS } from '../filters';
import { Box, Button, Chip } from '@mui/material';
import type { FilterState } from '../types';

// FilterChips lists the active filters, each removable.
export function FilterChips(props: {
  filter: FilterState;
  onChange: (f: FilterState) => void;
  onClear: () => void;
}): any {
  const { filter, onChange, onClear } = props;
  const chips: Array<{ key: string; label: string; remove: () => FilterState }> = [];
  for (const { key, label } of LIST_FILTERS) {
    for (const v of filter[key]) {
      chips.push({
        key: `${key}:${v}`,
        label: `${label}: ${v}`,
        remove: () => ({ ...filter, [key]: filter[key].filter((x) => x !== v) }),
      });
    }
  }
  for (const s of filter.severities) {
    chips.push({
      key: `severity:${s}`,
      label: `Severity: ${s}`,
      remove: () => ({ ...filter, severities: filter.severities.filter((x) => x !== s) }),
    });
  }
  if (filter.name.trim()) {
    chips.push({ key: 'name', label: `Name contains: ${filter.name.trim()}`, remove: () => ({ ...filter, name: '' }) });
  }
  if (filter.search.trim()) {
    chips.push({ key: 'search', label: `Search: ${filter.search.trim()}`, remove: () => ({ ...filter, search: '' }) });
  }
  if (chips.length === 0) return null;
  return h(
    Box,
    { sx: { display: 'flex', gap: 1, flexWrap: 'wrap', alignItems: 'center', mb: 2 } },
    ...chips.map((c) => h(Chip, { key: c.key, size: 'small', label: c.label, onDelete: () => onChange(c.remove()) })),
    h(Button, { size: 'small', onClick: onClear }, 'Clear filters')
  );
}
