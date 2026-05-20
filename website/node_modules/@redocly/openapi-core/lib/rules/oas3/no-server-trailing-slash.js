export const NoServerTrailingSlash = () => {
    return {
        Server(server, { report, location }) {
            if (!server.url)
                return;
            if (server.url.endsWith('/') && server.url !== '/') {
                report({
                    message: 'Server `url` should not have a trailing slash.',
                    location: location.child(['url']),
                });
            }
        },
    };
};
//# sourceMappingURL=no-server-trailing-slash.js.map