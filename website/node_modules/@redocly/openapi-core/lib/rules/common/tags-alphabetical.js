import { getTagName } from '../utils.js';
export const TagsAlphabetical = ({ ignoreCase = false }) => {
    return {
        Root(root, { report, location }) {
            if (!root.tags)
                return;
            for (let i = 0; i < root.tags.length - 1; i++) {
                if (getTagName(root.tags[i], ignoreCase) > getTagName(root.tags[i + 1], ignoreCase)) {
                    report({
                        message: 'The `tags` array should be in alphabetical order.',
                        location: location.child(['tags', i]),
                    });
                }
            }
        },
    };
};
//# sourceMappingURL=tags-alphabetical.js.map