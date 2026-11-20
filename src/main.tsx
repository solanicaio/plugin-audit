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
import { fetchEvents, fetchFacets, fetchStats } from './data';
import { Toolbar } from './components/Toolbar';
import { EventRow } from './components/EventRow';
import { EventDrawer } from './components/EventDrawer';
import { emptyFilter } from './types';
import type { AuditEvent, Facets, FilterState, Stats } from './types';

const SEARCH_DEBOUNCE_MS = 300;

function useDebounced<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = React.useState(value);
  React.useEffect(() => {
    const t = window.setTimeout(() => setDebounced(value), delay);
    return () => window.clearTimeout(t);
  }, [value, delay]);
  return debounced;
}

const AuditPage = (): any => {
  const [filter, setFilter] = React.useState<FilterState>(emptyFilter);
  const search = useDebounced(filter.search, SEARCH_DEBOUNCE_MS);
  const query = React.useMemo(() => ({ ...filter, search }), [
    filter.kinds,
    filter.reasons,
    filter.namespaces,
    filter.actors,
    filter.since,
    filter.until,
    search,
  ]);
  const [facets, setFacets] = React.useState<Facets>({
    kinds: [],
    reasons: [],
    namespaces: [],
    actors: [],
  });
  const [events, setEvents] = React.useState<AuditEvent[]>([]);
  const [stats, setStats] = React.useState<Stats | null>(null);
  const [next, setNext] = React.useState<string | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [selected, setSelected] = React.useState<AuditEvent | null>(null);
  const [reloads, setReloads] = React.useState(0);

  React.useEffect(() => {
    fetchFacets().then(setFacets).catch(() => {});
  }, [reloads]);

  React.useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    Promise.all([fetchEvents(query), fetchStats(query)])
      .then(([list, s]) => {
        if (cancelled) return;
        setEvents(list.items ?? []);
        setNext(list.next ?? null);
        setStats(s);
      })
      .catch((err: Error) => !cancelled && setError(err.message))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [query, reloads]);

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

  const filtered = JSON.stringify(query) !== JSON.stringify(emptyFilter);
  const summary = stats
    ? `${stats.total} ${filtered ? 'matching' : 'recorded'} event${stats.total === 1 ? '' : 's'}`
    : 'Loading…';

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
          Button,
          { variant: 'outlined', size: 'small', onClick: () => setReloads((n) => n + 1) },
          'Refresh'
        )
      ),

      error ? h(Alert, { severity: 'error', sx: { mb: 2 } }, `Failed to load events: ${error}`) : null,

      h(Toolbar, {
        filter,
        facets,
        onChange: setFilter,
        onReset: () => setFilter(emptyFilter),
      }),

      loading && events.length === 0
        ? h(Box, { sx: sx.empty }, 'Loading events…')
        : events.length === 0
        ? h(
            Box,
            { sx: sx.empty },
            filtered
              ? 'No events match the current filters.'
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

      selected ? h(EventDrawer, { event: selected, onClose: () => setSelected(null) }) : null
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
