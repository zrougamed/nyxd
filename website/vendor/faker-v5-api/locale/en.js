'use strict';

// postman-collection@5 requires('@faker-js/faker/locale/en') and expects the
// Faker 5 module shape. The real package is Faker 10 (fixed helpers.fake).
const { faker } = require('@faker-js/faker-upstream');

function bind(fn, ctx) {
  return function bound() {
    return fn.apply(ctx, arguments);
  };
}

const phoneFormats = ['###-###-####', '(###) ###-####', '1-###-###-####', '###.###.####'];

const api = {
  address: {
    city: bind(faker.location.city, faker.location),
    streetName: bind(faker.location.street, faker.location),
    streetAddress: bind(faker.location.streetAddress, faker.location),
    country: bind(faker.location.country, faker.location),
    countryCode: bind(faker.location.countryCode, faker.location),
    latitude: bind(faker.location.latitude, faker.location),
    longitude: bind(faker.location.longitude, faker.location),
    secondaryAddress: bind(faker.location.secondaryAddress, faker.location),
    state: bind(faker.location.state, faker.location),
    zipCode: bind(faker.location.zipCode, faker.location),
  },
  commerce: {
    color: bind(faker.color.human, faker.color),
    department: bind(faker.commerce.department, faker.commerce),
    productName: bind(faker.commerce.productName, faker.commerce),
    productAdjective: bind(faker.commerce.productAdjective, faker.commerce),
    productMaterial: bind(faker.commerce.productMaterial, faker.commerce),
    product: bind(faker.commerce.product, faker.commerce),
  },
  company: {
    companyName: bind(faker.company.name, faker.company),
    companySuffix: () => faker.helpers.arrayElement(['Inc', 'LLC', 'Group', 'and Sons']),
    catchPhrase: bind(faker.company.catchPhrase, faker.company),
    bs: bind(faker.company.buzzPhrase, faker.company),
    catchPhraseAdjective: bind(faker.company.catchPhraseAdjective, faker.company),
    catchPhraseDescriptor: bind(faker.company.catchPhraseDescriptor, faker.company),
    catchPhraseNoun: bind(faker.company.catchPhraseNoun, faker.company),
    bsAdjective: bind(faker.company.buzzAdjective, faker.company),
    bsBuzz: bind(faker.company.buzzVerb, faker.company),
    bsNoun: bind(faker.company.buzzNoun, faker.company),
  },
  database: {
    column: bind(faker.database.column, faker.database),
    type: bind(faker.database.type, faker.database),
    collation: bind(faker.database.collation, faker.database),
    engine: bind(faker.database.engine, faker.database),
  },
  date: {
    past: bind(faker.date.past, faker.date),
    future: bind(faker.date.future, faker.date),
    recent: bind(faker.date.recent, faker.date),
    month: bind(faker.date.month, faker.date),
    weekday: bind(faker.date.weekday, faker.date),
  },
  finance: {
    account: bind(faker.finance.accountNumber, faker.finance),
    accountName: bind(faker.finance.accountName, faker.finance),
    mask: bind(faker.finance.mask, faker.finance),
    amount: bind(faker.finance.amount, faker.finance),
    transactionType: bind(faker.finance.transactionType, faker.finance),
    currencyCode: () => faker.finance.currency().code,
    currencyName: () => faker.finance.currency().name,
    currencySymbol: () => faker.finance.currency().symbol,
    bitcoinAddress: bind(faker.finance.bitcoinAddress, faker.finance),
    iban: bind(faker.finance.iban, faker.finance),
    bic: bind(faker.finance.bic, faker.finance),
  },
  hacker: {
    abbreviation: bind(faker.hacker.abbreviation, faker.hacker),
    adjective: bind(faker.hacker.adjective, faker.hacker),
    noun: bind(faker.hacker.noun, faker.hacker),
    verb: bind(faker.hacker.verb, faker.hacker),
    ingverb: bind(faker.hacker.ingverb, faker.hacker),
    phrase: bind(faker.hacker.phrase, faker.hacker),
  },
  image: {
    imageUrl: bind(faker.image.url, faker.image),
    abstract: bind(faker.image.url, faker.image),
    animals: bind(faker.image.url, faker.image),
    business: bind(faker.image.url, faker.image),
    cats: bind(faker.image.url, faker.image),
    city: bind(faker.image.url, faker.image),
    food: bind(faker.image.url, faker.image),
    nightlife: bind(faker.image.url, faker.image),
    fashion: bind(faker.image.url, faker.image),
    people: bind(faker.image.url, faker.image),
    nature: bind(faker.image.url, faker.image),
    sports: bind(faker.image.url, faker.image),
    transport: bind(faker.image.url, faker.image),
    dataUri: bind(faker.image.dataUri, faker.image),
  },
  internet: {
    email: bind(faker.internet.email, faker.internet),
    exampleEmail: bind(faker.internet.exampleEmail, faker.internet),
    userName: bind(faker.internet.username, faker.internet),
    protocol: bind(faker.internet.protocol, faker.internet),
    url: bind(faker.internet.url, faker.internet),
    domainName: bind(faker.internet.domainName, faker.internet),
    domainSuffix: bind(faker.internet.domainSuffix, faker.internet),
    domainWord: bind(faker.internet.domainWord, faker.internet),
    ip: bind(faker.internet.ip, faker.internet),
    ipv6: bind(faker.internet.ipv6, faker.internet),
    userAgent: bind(faker.internet.userAgent, faker.internet),
    color: bind(faker.internet.color, faker.internet),
    mac: bind(faker.internet.mac, faker.internet),
    password: bind(faker.internet.password, faker.internet),
  },
  lorem: {
    word: bind(faker.lorem.word, faker.lorem),
    words: bind(faker.lorem.words, faker.lorem),
    sentence: bind(faker.lorem.sentence, faker.lorem),
    slug: bind(faker.lorem.slug, faker.lorem),
    sentences: bind(faker.lorem.sentences, faker.lorem),
    paragraph: bind(faker.lorem.paragraph, faker.lorem),
    paragraphs: bind(faker.lorem.paragraphs, faker.lorem),
    text: bind(faker.lorem.text, faker.lorem),
    lines: bind(faker.lorem.lines, faker.lorem),
  },
  name: {
    firstName: bind(faker.person.firstName, faker.person),
    lastName: bind(faker.person.lastName, faker.person),
    findName: bind(faker.person.fullName, faker.person),
    jobTitle: bind(faker.person.jobTitle, faker.person),
    prefix: bind(faker.person.prefix, faker.person),
    suffix: bind(faker.person.suffix, faker.person),
    jobDescriptor: bind(faker.person.jobDescriptor, faker.person),
    jobArea: bind(faker.person.jobArea, faker.person),
    jobType: bind(faker.person.jobType, faker.person),
  },
  phone: {
    phoneNumber: bind(faker.phone.number, faker.phone),
    phoneNumberFormat: (fmt) => {
      const pattern = phoneFormats[fmt] || phoneFormats[0];
      return faker.helpers.fromRegExp(pattern.replace(/#/g, '[0-9]'));
    },
  },
  system: {
    fileName: bind(faker.system.fileName, faker.system),
    commonFileName: bind(faker.system.commonFileName, faker.system),
    mimeType: bind(faker.system.mimeType, faker.system),
    commonFileType: bind(faker.system.commonFileType, faker.system),
    commonFileExt: bind(faker.system.commonFileExt, faker.system),
    fileType: bind(faker.system.fileType, faker.system),
    fileExt: bind(faker.system.fileExt, faker.system),
    semver: bind(faker.system.semver, faker.system),
  },
  datatype: {
    uuid: bind(faker.string.uuid, faker.string),
    boolean: bind(faker.datatype.boolean, faker.datatype),
    number: (opts) => {
      if (typeof opts === 'number') {
        return faker.number.int({ min: 0, max: opts });
      }
      if (opts && typeof opts === 'object') {
        return faker.number.int(opts);
      }
      return faker.number.int();
    },
  },
  random: {
    arrayElement: bind(faker.helpers.arrayElement, faker.helpers),
    word: bind(faker.lorem.word, faker.lorem),
    alphaNumeric: (count) => faker.string.alphanumeric(count || 1),
    words: bind(faker.lorem.words, faker.lorem),
  },
  helpers: {
    // Real Faker 10 helpers.fake (GHSA-qxc2-j82w-r537 fixed).
    fake: bind(faker.helpers.fake, faker.helpers),
    arrayElement: bind(faker.helpers.arrayElement, faker.helpers),
  },
  color: faker.color,
  location: faker.location,
  person: faker.person,
};

module.exports = api;
