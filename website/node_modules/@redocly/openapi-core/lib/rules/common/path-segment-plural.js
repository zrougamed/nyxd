import pluralize from 'pluralize';
import { isPathParameter } from '../../utils/is-path-parameter.js';
export const PathSegmentPlural = (opts) => {
    const { ignoreLastPathSegment, exceptions } = opts;
    return {
        PathItem: {
            leave(_path, { report, key, location }) {
                const pathKey = key.toString();
                if (pathKey.startsWith('/')) {
                    const pathSegments = pathKey.split('/');
                    pathSegments.shift();
                    if (ignoreLastPathSegment && pathSegments.length > 0) {
                        pathSegments.pop();
                    }
                    for (const pathSegment of pathSegments) {
                        if (exceptions && exceptions.includes(pathSegment))
                            continue;
                        if (!isPathParameter(pathSegment) && pluralize.isSingular(pathSegment)) {
                            report({
                                message: `path segment \`${pathSegment}\` should be plural.`,
                                location: location.key(),
                            });
                        }
                    }
                }
            },
        },
    };
};
//# sourceMappingURL=path-segment-plural.js.map