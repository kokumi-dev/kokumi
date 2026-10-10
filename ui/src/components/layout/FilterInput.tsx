import styles from './layout.module.css'

interface Props {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  label: string
}

export default function FilterInput({ value, onChange, placeholder = 'Filter…', label }: Props) {
  return (
    <input
      className={styles.filterInput}
      type="search"
      placeholder={placeholder}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      aria-label={label}
    />
  )
}
