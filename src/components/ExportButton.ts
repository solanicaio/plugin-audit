import { h, React } from '../runtime';
import { MAX_EXPORT } from '../data';
import { Button, ListItemText, Menu, MenuItem } from '@mui/material';

export function ExportButton(props: {
  total: number;
  onExport: (format: 'csv' | 'json') => Promise<void>;
}): any {
  const { total, onExport } = props;
  const [anchor, setAnchor] = React.useState<HTMLElement | null>(null);
  const [busy, setBusy] = React.useState(false);
  const run = (format: 'csv' | 'json') => {
    setAnchor(null);
    setBusy(true);
    onExport(format).finally(() => setBusy(false));
  };
  const note =
    total > MAX_EXPORT
      ? `The newest ${MAX_EXPORT.toLocaleString()} of ${total.toLocaleString()} events`
      : `${total.toLocaleString()} event${total === 1 ? '' : 's'}`;
  return [
    h(
      Button,
      {
        key: 'button',
        variant: 'outlined',
        size: 'small',
        disabled: busy || total === 0,
        onClick: (e: any) => setAnchor(e.currentTarget),
        'aria-haspopup': 'menu',
      },
      busy ? 'Exporting…' : 'Export'
    ),
    h(
      Menu,
      { key: 'menu', anchorEl: anchor, open: !!anchor, onClose: () => setAnchor(null) },
      h(MenuItem, { onClick: () => run('csv') }, h(ListItemText, { primary: 'CSV', secondary: note })),
      h(MenuItem, { onClick: () => run('json') }, h(ListItemText, { primary: 'JSON', secondary: note }))
    ),
  ];
}
