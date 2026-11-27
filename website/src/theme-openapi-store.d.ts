// docusaurus-theme-openapi-docs ships types for its Redux store but does not
// declare the @theme/ApiItem/store alias that src/theme/Root.tsx imports.
declare module '@theme/ApiItem/store' {
  export * from 'docusaurus-theme-openapi-docs/lib/theme/ApiItem/store';
}
