import layout from '../layout/layout.module.css'

export default function Counter({ count }: { count?: number }) {
  if (!count) return null
  return <span className={layout.counter}>{count}</span>
}
