import commonEn from './en/common';
import authEn from './en/auth';
import dashboardEn from './en/dashboard';
import trucksEn from './en/trucks';
import maintenanceEn from './en/maintenance';
import usersEn from './en/users';
import assignmentsEn from './en/assignments';
import renewalsEn from './en/renewals';
import subcontractorsEn from './en/subcontractors';
import waitlistEn from './en/waitlist';
import landingEn from './en/landing';

import commonTh from './th/common';
import authTh from './th/auth';
import dashboardTh from './th/dashboard';
import trucksTh from './th/trucks';
import maintenanceTh from './th/maintenance';
import usersTh from './th/users';
import assignmentsTh from './th/assignments';
import renewalsTh from './th/renewals';
import subcontractorsTh from './th/subcontractors';
import waitlistTh from './th/waitlist';
import landingTh from './th/landing';

export const translations = {
    en: {
        ...commonEn,
        ...authEn,
        ...dashboardEn,
        ...trucksEn,
        ...maintenanceEn,
        ...usersEn,
        ...assignmentsEn,
        ...renewalsEn,
        ...subcontractorsEn,
        ...waitlistEn,
        ...landingEn,
    },
    th: {
        ...commonTh,
        ...authTh,
        ...dashboardTh,
        ...trucksTh,
        ...maintenanceTh,
        ...usersTh,
        ...assignmentsTh,
        ...renewalsTh,
        ...subcontractorsTh,
        ...waitlistTh,
        ...landingTh,
    },
};
