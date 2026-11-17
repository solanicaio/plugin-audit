// Styling via MUI `sx` + theme tokens (palette paths, spacing units). The theme
// comes from PluginThemeProvider, which reads the host's `--everest-*` CSS
// variables, so the plugin follows the host palette and dark mode.
import type { SxProps, Theme } from '@mui/material';

export type MuiColor =
  | 'default'
  | 'primary'
  | 'secondary'
  | 'error'
  | 'info'
  | 'success'
  | 'warning';

export const sx = {
  page: { p: 3, maxWidth: 1280, mx: 'auto' },
  headerRow: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 2,
    mb: 2,
    flexWrap: 'wrap',
  },
  subtitle: { color: 'text.secondary' },
  toolbar: {
    display: 'flex',
    gap: 1.5,
    alignItems: 'center',
    flexWrap: 'wrap',
    p: 1.5,
    bgcolor: 'background.paper',
    border: 1,
    borderColor: 'divider',
    borderRadius: 1,
    mb: 2,
  },
  filter: { flex: '1 1 180px', minWidth: 160 },
  count: { color: 'text.secondary', fontSize: '0.75rem', ml: 2, fontVariantNumeric: 'tabular-nums' },
  chart: { p: 2, pb: 1, mb: 2 },
  controls: { display: 'flex', gap: 1.5, alignItems: 'center', flexWrap: 'wrap', mb: 2 },
  empty: { p: 6, textAlign: 'center', color: 'text.secondary' },
  nowrap: { whiteSpace: 'nowrap' },
  message: {
    maxWidth: 360,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  },
  more: { display: 'flex', justifyContent: 'center', mt: 2 },
  section: { mt: 2.5 },
  sectionTitle: {
    textTransform: 'uppercase',
    color: 'text.secondary',
    letterSpacing: '0.05em',
    fontSize: '0.75rem',
    mb: 1,
  },
  drawerHeader: { display: 'flex', alignItems: 'flex-start', gap: 1.5, mb: 2 },
  detailRow: { display: 'flex', gap: 1, alignItems: 'baseline', mb: 0.5 },
  detailKey: { color: 'text.secondary', fontWeight: 500, minWidth: 120, flexShrink: 0 },
  codeBlock: {
    bgcolor: 'grey.900',
    color: 'grey.100',
    p: 1.5,
    m: 0,
    borderRadius: 1,
    fontFamily: 'monospace',
    fontSize: '0.8125rem',
    whiteSpace: 'pre-wrap',
    overflowWrap: 'anywhere',
  },
} satisfies Record<string, SxProps<Theme>>;

// Warnings (failed logins, failed phases) stand out; otherwise the reason
// tells the lifecycle step.
export function reasonColor(reason?: string, severity?: string): MuiColor {
  if (severity === 'Warning') return 'warning';
  switch (reason) {
    case 'Created':
    case 'LoginSucceeded':
      return 'success';
    case 'DeletionRequested':
    case 'Deleted':
    case 'LoginFailed':
      return 'error';
    case 'PhaseChanged':
      return 'info';
    default:
      return 'default';
  }
}

export function formatTime(iso?: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}
