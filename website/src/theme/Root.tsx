import React from 'react';
import {Provider} from 'react-redux';
import {createStoreWithoutState} from '@theme/ApiItem/store';

// Global Redux store required by docusaurus-theme-openapi-docs.
// Must be a single stable instance so the Provider context is always present.
const store = createStoreWithoutState({}, []);

export default function Root({children}: {children: React.ReactNode}) {
  return <Provider store={store}>{children}</Provider>;
}
