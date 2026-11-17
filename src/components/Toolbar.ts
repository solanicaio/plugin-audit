import { h } from '../runtime';
import { sx } from '../styles';
import { Autocomplete, Box, Button, TextField } from '@mui/material';
import type { Facets, FilterState } from '../types';

type ListKey = 'kinds' | 'reasons' | 'namespaces' | 'actors';

const LIST_FILTERS: Array<{ key: ListKey; label: string }> = [
  { key: 'kinds', label: 'Kinds' },
  { key: 'reasons', label: 'Reasons' },
  { key: 'namespaces', label: 'Namespaces' },
  { key: 'actors', label: 'Actors' },
];

export function Toolbar(props: {
  filter: FilterState;
  facets: Facets;
  onChange: (f: FilterState) => void;
  onReset: () => void;
}): any {
  const { filter, facets, onChange, onReset } = props;
  return h(
    Box,
    { sx: sx.toolbar },
    h(TextField, {
      size: 'small',
      type: 'search',
      placeholder: 'Search by name or message…',
      value: filter.search,
      onChange: (e: any) => onChange({ ...filter, search: e.target.value }),
      sx: { flex: '2 1 240px', minWidth: 200 },
    }),
    ...LIST_FILTERS.map(({ key, label }) =>
      h(Autocomplete, {
        key,
        multiple: true,
        size: 'small',
        limitTags: 1,
        options: facets[key],
        value: filter[key],
        onChange: (_: unknown, value: string[]) => onChange({ ...filter, [key]: value }),
        renderInput: (params: any) => h(TextField, { ...params, label }),
        sx: sx.filter,
      })
    ),
    ...(['since', 'until'] as const).map((key) =>
      h(TextField, {
        key,
        size: 'small',
        type: 'datetime-local',
        label: key === 'since' ? 'From' : 'To',
        value: filter[key],
        onChange: (e: any) => onChange({ ...filter, [key]: e.target.value }),
        slotProps: { inputLabel: { shrink: true } },
        sx: { flex: '1 1 200px' },
      })
    ),
    h(Button, { size: 'small', onClick: onReset }, 'Reset')
  );
}
