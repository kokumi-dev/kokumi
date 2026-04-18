import yaml from 'js-yaml'

/**
 * Serialises a plain JS object to a YAML string.
 * Returns an empty string for null/empty objects.
 */
export function objectToYaml(values: Record<string, unknown>): string {
  if (!values || Object.keys(values).length === 0) return ''
  return yaml.dump(values, { lineWidth: 100 }).trimEnd()
}

/**
 * Parses a YAML string into a plain JS object (mapping).
 * Returns {} for empty/null input.
 * Throws if the input is not a mapping.
 */
export function yamlToValues(text: string): Record<string, unknown> {
  if (!text.trim()) return {}
  const parsed = yaml.load(text)
  if (parsed == null) return {}
  if (typeof parsed !== 'object' || Array.isArray(parsed))
    throw new Error('Values must be a YAML mapping')
  return parsed as Record<string, unknown>
}
