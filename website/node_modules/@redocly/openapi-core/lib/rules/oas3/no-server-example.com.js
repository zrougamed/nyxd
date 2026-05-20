export const NoServerExample = () => {
    return {
        Server(server, { report, location }) {
            // eslint-disable-next-line no-useless-escape
            const pattern = /^(.*[\/.])?(example\.com|localhost)([\/:?].*|$)/;
            if (server.url && pattern.test(server.url)) {
                report({
                    message: 'Server `url` should not point to example.com or localhost.',
                    location: location.child(['url']),
                });
            }
        },
    };
};
//# sourceMappingURL=no-server-example.com.js.map