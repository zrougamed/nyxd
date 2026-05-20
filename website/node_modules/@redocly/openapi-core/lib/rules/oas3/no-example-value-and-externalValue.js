export const NoExampleValueAndExternalValue = () => {
    return {
        Example(example, { report, location }) {
            if (example.value && example.externalValue) {
                report({
                    message: 'Example object can have either `value` or `externalValue` fields.',
                    location: location.child(['value']).key(),
                });
            }
        },
    };
};
//# sourceMappingURL=no-example-value-and-externalValue.js.map