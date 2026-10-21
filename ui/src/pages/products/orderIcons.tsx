import { NodeIcon } from '../../components/graph/parts'

/** The icon of an Order, as in the navigation; muted while it runs nothing. */
export function OrderIcon({ size, muted }: { size?: 'md' | 'lg'; muted?: boolean }) {
  return (
    <NodeIcon tone={muted ? 'muted' : 'accent'} size={size}>
      <path d="M4.5 1.5v13M4.5 5.25h5a2.75 2.75 0 0 1 0 5.5h-5" />
    </NodeIcon>
  )
}
