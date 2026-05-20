export const SourceDescriptionsNameUnique = () => {
    const seenSourceDescriptions = new Set();
    return {
        SourceDescriptions: {
            enter(sourceDescriptions, { report, location }) {
                if (!sourceDescriptions.length)
                    return;
                for (const sourceDescription of sourceDescriptions) {
                    if (seenSourceDescriptions.has(sourceDescription.name)) {
                        report({
                            message: 'The `name` must be unique amongst all SourceDescriptions.',
                            location: location.child([sourceDescriptions.indexOf(sourceDescription)]),
                        });
                    }
                    seenSourceDescriptions.add(sourceDescription.name);
                }
            },
        },
    };
};
//# sourceMappingURL=sourceDescriptions-name-unique.js.map