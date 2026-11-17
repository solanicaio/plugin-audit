// Frontend bundle for the OpenEverest audit plugin.
//
// Registers a sidebar entry and a route that renders the audit log: the
// lifecycle events recorded by the plugin's Go backend, with server-side
// filters and a detail drawer.
//
// React and the host-authenticated fetch are injected via the `register(api)`
// call (see runtime.ts). The UI uses the plugin's own pinned MUI, themed by
// @openeverest/plugin-theme from the host's `--everest-*` CSS variables, so
// host MUI upgrades don't affect it.
import type { PluginRegisterFn, PluginApi } from '@openeverest/plugin-sdk';

import { React, h, initRuntime, cssNonce } from './runtime';
import { sx } from './styles';
import { PluginThemeProvider } from '@openeverest/plugin-theme';
import {
  Alert,
  Box,
  Button,
  Paper,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Typography,
} from '@mui/material';
import { exportEvents, fetchEvents, fetchFacets, fetchHistogram, fetchStats, toQuery } from './data';
import { hasFilters, readURL, writeURL } from './filters';
import { Toolbar } from './components/Toolbar';
import { TimeRange } from './components/TimeRange';
import { EventsChart } from './components/EventsChart';
import { FilterChips } from './components/FilterChips';
import { ExportButton } from './components/ExportButton';
import { EventRow } from './components/EventRow';
import { EventDrawer } from './components/EventDrawer';
import { emptyFacets, emptyFilter } from './types';
import type { AuditEvent, Facets, FilterState, Histogram, Severity, Stats } from './types';

const TYPING_DEBOUNCE_MS = 300;

function useDebounced<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = React.useState(value);
  React.useEffect(() => {
    const t = window.setTimeout(() => setDebounced(value), delay);
    return () => window.clearTimeout(t);
  }, [value, delay]);
  return debounced;
}

const AuditPage = (): any => {
  const [filter, setFilter] = React.useState<FilterState>(() => readURL(window.location.search));
  // Typing in the text fields doesn't query on every key.
  const name = useDebounced(filter.name, TYPING_DEBOUNCE_MS);
  const search = useDebounced(filter.search, TYPING_DEBOUNCE_MS);
  const committed = { ...filter, name, search };
  const key = JSON.stringify(committed);
  const [reloads, setReloads] = React.useState(0);
  // Relative ranges are resolved when the filter changes or on refresh.
  const query = React.useMemo(() => toQuery(committed, Date.now()), [key, reloads]);

  const [facets, setFacets] = React.useState<Facets>(emptyFacets);
  const [events, setEvents] = React.useState<AuditEvent[]>([]);
  const [stats, setStats] = React.useState<Stats | null>(null);
  const [histogram, setHistogram] = React.useState<Histogram | null>(null);
  const [next, setNext] = React.useState<string | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [selected, setSelected] = React.useState<AuditEvent | null>(null);

  // The filter is kept in the page URL, so the view can be shared.
  React.useEffect(() => {
    const { pathname, search: current, hash } = window.location;
    const updated = writeURL(committed, current);
    if (updated !== current) {
      window.history.replaceState(window.history.state, '', pathname + updated + hash);
    }
  }, [key]);

  React.useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    fetchFacets(query)
      .then((f) => !cancelled && setFacets(f))
      .catch(() => {});
    Promise.all([fetchEvents(query), fetchStats(query), fetchHistogram(query)])
      .then(([list, s, hist]) => {
        if (cancelled) return;
        setEvents(list.items ?? []);
        setNext(list.next ?? null);
        setStats(s);
        setHistogram(hist);
      })
      .catch((err: Error) => !cancelled && setError(err.message))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [query]);

  const loadMore = () => {
    if (next == null) return;
    setLoading(true);
    fetchEvents(query, next)
      .then((list) => {
        setEvents((prev) => [...prev, ...(list.items ?? [])]);
        setNext(list.next ?? null);
      })
      .catch((err: Error) => setError(err.message))
      .finally(() => setLoading(false));
  };

  const toggleSeverity = (s: Severity) =>
    setFilter((f) => ({ ...f, severities: f.severities.includes(s) ? [] : [s] }));
  const zoom = (since: string, until: string) => setFilter((f) => ({ ...f, range: 'custom', since, until }));
  const clearFilters = () => setFilter((f) => ({ ...emptyFilter, range: f.range, since: f.since, until: f.until }));
  const doExport = (format: 'csv' | 'json') =>
    exportEvents(query, format).catch((err: Error) => setError(`Export failed: ${err.message}`));

  const filtered = hasFilters(committed);
  const total = stats?.total ?? 0;
  const noun = `${filtered ? 'matching ' : ''}event${total === 1 ? '' : 's'}`;
  const summary = stats ? `${total.toLocaleString()} ${noun} in this time range` : 'Loading…';

  return h(
    PluginThemeProvider,
    { cacheKey: 'plugin-audit', nonce: cssNonce },
    h(
      Box,
      { sx: sx.page, 'data-testid': 'plugin-audit-root' },
      h(
        Box,
        { sx: sx.headerRow },
        h(
          Box,
          null,
          h(Typography, { variant: 'h5', sx: { fontWeight: 600 } }, 'Audit Log'),
          h(
            Typography,
            { variant: 'body2', sx: sx.subtitle },
            `Lifecycle events of databases, backups, plugins and logins. ${summary}.`
          )
        ),
        h(
          Box,
          { sx: { display: 'flex', gap: 1 } },
          h(ExportButton, { total, onExport: doExport }),
          h(Button, { variant: 'outlined', size: 'small', onClick: () => setReloads((n) => n + 1) }, 'Refresh')
        )
      ),

      error ? h(Alert, { severity: 'error', sx: { mb: 2 } }, error) : null,

      h(Box, { sx: sx.controls }, h(TimeRange, { filter, onChange: setFilter })),

      h(EventsChart, {
        histogram,
        severities: filter.severities,
        onToggleSeverity: toggleSeverity,
        onZoom: zoom,
      }),

      h(Toolbar, { filter, facets, onChange: setFilter }),
      h(FilterChips, { filter, onChange: setFilter, onClear: clearFilters }),

      loading && events.length === 0
        ? h(Box, { sx: sx.empty }, 'Loading events…')
        : events.length === 0
        ? h(
            Box,
            { sx: sx.empty },
            filtered || filter.range !== 'all'
              ? 'No events match the current filters and time range.'
              : 'No events yet. Lifecycle events appear here as they are recorded.'
          )
        : h(
            TableContainer,
            { component: Paper, variant: 'outlined' },
            h(
              Table,
              { size: 'small' },
              h(
                TableHead,
                null,
                h(
                  TableRow,
                  null,
                  h(TableCell, null, 'Time'),
                  h(TableCell, null, 'Event'),
                  h(TableCell, null, 'Object'),
                  h(TableCell, null, 'Namespace'),
                  h(TableCell, null, 'Actor'),
                  h(TableCell, null, 'Message')
                )
              ),
              h(
                TableBody,
                null,
                ...events.map((event) => EventRow({ event, onSelect: setSelected }))
              )
            )
          ),

      next != null
        ? h(
            Box,
            { sx: sx.more },
            h(Button, { size: 'small', onClick: loadMore, disabled: loading }, loading ? 'Loading…' : 'Load more')
          )
        : null,

      selected
        ? h(EventDrawer, {
            event: selected,
            onClose: () => setSelected(null),
            onShowInstance: (instance: string) => {
              setSelected(null);
              setFilter((f) => ({ ...f, name: instance }));
            },
          })
        : null
    )
  );
};

const register: PluginRegisterFn = (api: PluginApi) => {
  initRuntime(api);

  api.registerExtension({
    type: 'sidebarItem',
    label: 'Audit Log',
  });

  api.registerExtension({
    type: 'route',
    label: 'Audit Log',
    component: AuditPage,
  });
};

export default register;
