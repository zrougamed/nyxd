import { getTagName } from '../utils.js';
export const NoDuplicatedTagNames = ({ ignoreCase = false, }) => {
    const tagNames = new Set();
    return {
        Tag: {
            leave(tag, ctx) {
                const tagName = getTagName(tag, ignoreCase);
                if (tagNames.has(tagName)) {
                    ctx.report({
                        message: `Duplicate tag name found: '${tag.name}'.`,
                        location: ctx.location,
                    });
                }
                else {
                    tagNames.add(tagName);
                }
            },
        },
    };
};
//# sourceMappingURL=no-duplicated-tag-names.js.map