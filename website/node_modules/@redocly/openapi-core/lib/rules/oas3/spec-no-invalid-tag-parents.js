export const SpecNoInvalidTagParents = () => {
    let tags;
    return {
        Root: {
            enter(root) {
                tags = root.tags ?? [];
            },
        },
        Tag: {
            leave(tag, { report, location }) {
                if (tag?.parent === undefined) {
                    return;
                }
                if (!tags.find((t) => t.name === tag.parent)) {
                    report({
                        message: `Tag parent '${tag.parent}' is not defined in the API description.`,
                        location: location.child('parent'),
                    });
                    return;
                }
                const visited = new Set(tag.name);
                let currentParent = tag.parent;
                while (currentParent !== undefined) {
                    if (visited.has(currentParent)) {
                        report({
                            message: `Circular reference detected in tag parent hierarchy for tag '${tag.name}'.`,
                            location: location.child('parent'),
                        });
                        return;
                    }
                    visited.add(currentParent);
                    currentParent = tags.find((t) => t.name === currentParent)?.parent;
                }
            },
        },
    };
};
//# sourceMappingURL=spec-no-invalid-tag-parents.js.map