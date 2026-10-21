/**
 * The colour of a status, independent of its words: a success, something
 * under way or held, a failure, something a person should act on, or nothing
 * known. Badges, status dots and the marks of graph nodes share it.
 */
export type Tone = 'success' | 'warning' | 'danger' | 'accent' | 'neutral'
