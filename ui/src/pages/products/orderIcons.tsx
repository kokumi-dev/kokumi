import { NodeIcon } from '../../components/graph/parts'

/** The icon of a Product, as in the navigation. */
export function ProductIcon() {
  return (
    <NodeIcon tone="accent">
      <path d="M8 1.75 13.5 4.6v6.8L8 14.25 2.5 11.4V4.6L8 1.75Z" />
      <path d="M2.5 4.6 8 7.5l5.5-2.9M8 7.5v6.75" />
    </NodeIcon>
  )
}

/** The icon of an Order, as in the navigation; muted while it runs nothing. */
export function OrderIcon({ size, muted }: { size?: 'md' | 'lg'; muted?: boolean }) {
  return (
    <NodeIcon tone={muted ? 'muted' : 'accent'} size={size}>
      <path d="M4.5 1.5v13M4.5 5.25h5a2.75 2.75 0 0 1 0 5.5h-5" />
    </NodeIcon>
  )
}
