export const SourceDescriptionType = () => {
    return {
        SourceDescriptions: {
            enter(sourceDescriptions, { report, location }) {
                if (!sourceDescriptions.length)
                    return;
                for (const sourceDescription of sourceDescriptions) {
                    if (!['openapi', 'arazzo'].includes(sourceDescription?.type)) {
                        report({
                            message: 'The `type` property of the `sourceDescription` object must be either `openapi` or `arazzo`.',
                            location: location.child([sourceDescriptions.indexOf(sourceDescription)]),
                        });
                    }
                }
            },
        },
    };
};
//# sourceMappingURL=sourceDescription-type.js.map