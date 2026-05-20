import { validateDefinedAndNonEmpty } from '../utils.js';
export const TagDescription = () => {
    return {
        Tag(tag, ctx) {
            validateDefinedAndNonEmpty('description', tag, ctx);
        },
    };
};
//# sourceMappingURL=tag-description.js.map