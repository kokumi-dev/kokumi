/** Colors must come from the design tokens in src/index.css. */
export default {
  rules: {
    'color-no-hex': true,
    'color-named': 'never',
    'function-disallowed-list': ['rgb', 'rgba', 'hsl', 'hsla', 'hwb'],
  },
  overrides: [
    {
      files: ['src/index.css', 'src/components/shared/DiffView.module.css'],
      rules: {
        'color-no-hex': null,
        'color-named': null,
        'function-disallowed-list': null,
      },
    },
  ],
}
