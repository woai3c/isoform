/**
 * Generates a random adjective-animal name like "brave-fox" or "gentle-tiger".
 * Lightweight alternative to unique-names-generator.
 */

const adjectives = [
    'brave', 'bright', 'calm', 'clever', 'cool', 'crisp', 'daring', 'eager',
    'fair', 'fast', 'fierce', 'gentle', 'grand', 'happy', 'keen', 'kind',
    'lively', 'lucky', 'merry', 'neat', 'noble', 'proud', 'quick', 'quiet',
    'rapid', 'sharp', 'silent', 'smart', 'smooth', 'soft', 'solid', 'steady',
    'strong', 'sturdy', 'sure', 'swift', 'vivid', 'warm', 'wild', 'wise',
];

const animals = [
    'ant', 'bear', 'bird', 'cat', 'deer', 'dog', 'dolphin', 'dove', 'eagle',
    'elk', 'falcon', 'finch', 'fox', 'frog', 'hawk', 'heron', 'horse',
    'husky', 'ibis', 'jay', 'koala', 'lark', 'lion', 'lynx', 'mole',
    'moth', 'newt', 'okapi', 'orca', 'otter', 'owl', 'panda', 'parrot',
    'puma', 'quail', 'raven', 'robin', 'seal', 'shark', 'snake', 'sparrow',
    'squid', 'stork', 'swan', 'tiger', 'toad', 'trout', 'viper', 'whale',
    'wolf', 'wren', 'zebra',
];

function pick<T>(arr: T[]): T {
    return arr[Math.floor(Math.random() * arr.length)];
}

export function randomName(): string {
    return `${pick(adjectives)}-${pick(animals)}`;
}
