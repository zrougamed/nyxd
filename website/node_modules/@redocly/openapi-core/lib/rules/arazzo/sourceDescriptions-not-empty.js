export const SourceDescriptionsNotEmpty = () => {
    return {
        SourceDescriptions: {
            enter(sourceDescriptions, { report, location }) {
                if (!sourceDescriptions?.length) {
                    report({
                        message: 'The `sourceDescriptions` list must have at least one entry.',
                        location,
                    });
                }
            },
        },
    };
};
//# sourceMappingURL=sourceDescriptions-not-empty.js.map