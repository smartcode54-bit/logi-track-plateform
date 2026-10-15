// Go internal/fleet.ValidPlate: 6–8 characters, two Thai consonants with an optional leading digit.
export const TRUCK_PLATE_PATTERN = "([ก-ฮ]{2}|[0-9][ก-ฮ]{2})-?[0-9]{1,4}";
export const TRUCK_PLATE_MAX_LENGTH = 8;
export const TRUCK_PLATE_REGEX = new RegExp(`^${TRUCK_PLATE_PATTERN}$`);
