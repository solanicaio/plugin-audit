import { h } from '../runtime';
import { sx } from '../styles';
import { LIST_FILTERS } from '../filters';
import { Autocomplete, Box, TextField } from '@mui/material';
import type { FacetValue, Facets, FilterState } from '../types';

// The facet values, with the selected ones that no event matches anymore.
function options(values: FacetValue[], selected: string[]): FacetValue[] {
  const missing = selected.filter((v) => !values.some((o) => o.value === v));
  return [...values, ...missing.map((value) => ({ value, count: 0 }))];
}

export function Toolbar(props: {
  filter: FilterState;
  facets: Facets;
  onChange: (f: FilterState) => void;
}): any {
  const { filter, facets, onChange } = props;
  return h(
    Box,
    { sx: sx.toolbar },
    h(TextField, {
      size: 'small',
      label: 'Name',
      placeholder: 'Database or object name',
      value: filter.name,
      onChange: (e: any) => onChange({ ...filter, name: e.target.value }),
      sx: sx.filter,
    }),
    // Engines are known for events of core versions that report them.
    ...LIST_FILTERS.filter(({ key }) => key !== 'providers' || facets.providers.length > 0 || filter.providers.length > 0).map(
      ({ key, label }) =>
        h(Autocomplete, {
          key,
          multiple: true,
          size: 'small',
          limitTags: 1,
          options: options(facets[key], filter[key]),
          value: filter[key].map((value) => ({ value, count: 0 })),
          isOptionEqualToValue: (o: FacetValue, v: FacetValue) => o.value === v.value,
          getOptionLabel: (o: FacetValue) => o.value,
          onChange: (_: unknown, value: FacetValue[]) => onChange({ ...filter, [key]: value.map((v) => v.value) }),
          renderOption: (props: any, o: FacetValue) => {
            const { key: optionKey, ...rest } = props;
            return h(
              'li',
              { key: optionKey, ...rest },
              h(Box, { component: 'span', sx: { flex: 1, overflowWrap: 'anywhere' } }, o.value),
              h(Box, { component: 'span', sx: sx.count }, o.count.toLocaleString())
            );
          },
          renderInput: (params: any) => h(TextField, { ...params, label }),
          sx: sx.filter,
        })
    ),
    h(TextField, {
      size: 'small',
      type: 'search',
      label: 'Search',
      placeholder: 'Any text, e.g. a message',
      value: filter.search,
      onChange: (e: any) => onChange({ ...filter, search: e.target.value }),
      sx: sx.filter,
    })
  );
}
