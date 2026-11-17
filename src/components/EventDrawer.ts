import { h } from '../runtime';
import { formatTime, reasonColor, sx } from '../styles';
import { DrawerShell } from './DrawerShell';
import { Box, Button, Chip, Divider, IconButton, Typography } from '@mui/material';
import type { AuditEvent } from '../types';

function detail(label: string, value?: string): any {
  if (!value) return null;
  return h(
    Box,
    { key: label, sx: sx.detailRow },
    h(Typography, { component: 'span', variant: 'body2', sx: sx.detailKey }, label),
    h(Typography, { component: 'span', variant: 'body2', sx: { overflowWrap: 'anywhere' } }, value)
  );
}

const sectionTitle = (label: string): any =>
  h(Typography, { variant: 'overline', component: 'div', sx: sx.sectionTitle }, label);

export function EventDrawer(props: {
  event: AuditEvent;
  onClose: () => void;
  // Filters the log by an instance name.
  onShowInstance: (name: string) => void;
}): any {
  const { event, onClose, onShowInstance } = props;
  const env = event.envelope ?? {};
  const phase = env.phase && (env.phase.from || env.phase.to)
    ? `${env.phase.from || '—'} → ${env.phase.to || '—'}`
    : undefined;
  const object = event.resourceKind
    ? `${event.resourceKind} ${event.namespace ? `${event.namespace}/` : ''}${event.resourceName ?? ''}`
    : undefined;
  const actor = event.actorID
    ? `${event.actorID}${event.actorType ? ` (${event.actorType})` : ''}`
    : undefined;

  return h(
    DrawerShell,
    { onClose },
    h(
      Box,
      { sx: sx.drawerHeader },
      h(
        Box,
        { sx: { minWidth: 0 } },
        h(
          Typography,
          { variant: 'h6', sx: { fontWeight: 600, overflowWrap: 'anywhere' } },
          event.resourceName || event.type
        ),
        h(
          Box,
          { sx: { display: 'flex', alignItems: 'center', gap: 0.75, mt: 0.25 } },
          h(Chip, {
            label: event.reason || event.type,
            size: 'small',
            color: reasonColor(event.reason, event.severity),
            variant: 'outlined',
          }),
          h(
            Typography,
            { variant: 'caption', sx: { color: 'text.secondary' } },
            formatTime(event.occurredAt)
          )
        )
      ),
      h(IconButton, { onClick: onClose, 'aria-label': 'Close', sx: { ml: 'auto' } }, '×')
    ),
    event.message
      ? h(Typography, { variant: 'body2', sx: { whiteSpace: 'pre-line', mb: 2 } }, event.message)
      : null,
    h(
      Box,
      null,
      sectionTitle('Details'),
      detail('Type', event.type),
      detail('Severity', event.severity),
      detail('Object', object),
      detail('Instance', event.instance),
      detail('Engine', event.provider),
      detail('Phase', phase),
      detail('Actor', actor),
      detail('Source IP', typeof env.sourceIP === 'string' ? env.sourceIP : undefined),
      detail('Cluster', event.cluster),
      detail('Event', event.name),
      detail('Cursor', event.cursor)
    ),
    event.instance
      ? h(
          Button,
          { size: 'small', sx: { mt: 1 }, onClick: () => onShowInstance(event.instance!) },
          `Show events of ${event.instance}`
        )
      : null,
    h(Divider, { sx: { my: 2 } }),
    sectionTitle('Raw event'),
    h(Box, { component: 'pre', sx: sx.codeBlock }, JSON.stringify(event.envelope, null, 2))
  );
}
