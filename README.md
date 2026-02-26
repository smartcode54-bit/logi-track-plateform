# Logitrack Platform

Logitrack-platform is a comprehensive, dual-application logistics ecosystem designed to bridge the gap between field operations (drivers) and central management (admins).

## 📱 Logitrack Mobile: The Driver’s Command Center
The mobile application (built with Flutter) focuses on high-integrity data collection and task execution.

- **Intelligent Task Management**:
  - **Real-time Synchronization**: Drivers receive instant updates for "First Mile" (FM) and "Line Haul" (LH) tasks via Firestore streams.
  - **Workflow Guardrails**: Features logic to prevent "Check-in" to new tasks if an ongoing task remains unfinished, ensuring a strictly sequential operational flow.
  - **Manual Task Initiation**: Allows drivers to manually "Check-in" for Line Haul tasks by selecting predefined SOCs (Origins) and Hubs (Destinations).
- **Proof of Action (Evidence Collection)**:
  - **Geo-Tagged Evidence**: Every check-in requires a photo captured in real-time. The system automatically attaches GPS coordinates, timestamps, and address data to the image.
  - **Interactive History**: Drivers can view their trip history, including high-resolution images they previously uploaded and map links to the exact check-in locations.
- **Vehicle & Fleet Tracking**: Tracks specific truck types (e.g., PICKUP, 4WJ, 6WH, 10WH, Van) and current truck assignments linked to the driver's profile.
- **Modern UX**: Fully bilingual support (English/Thai) and an adaptive Dark/Light mode for optimized visibility during night or day driving.

## 🌐 Logitrack Web: The Admin Control Panel
The web application (built with Next.js) serves as the "brain," providing centralized oversight and configuration.

- **Real-Time Dashboard**:
  - **Job Monitor**: A dedicated interface to monitor all trip records, categorized by job type (First Mile vs. Line Haul).
  - **System Statistics**: Overview of total users, active drivers, and package statuses at a glance.
- **Advanced Security & Administration**:
  - **Secure Auth Architecture**: Uses Google Authentication with a sophisticated cookie-based session management system and Next.js Middleware for route protection.
  - **Role-Based Access (RBAC)**: Automatically recognizes administrators based on an email whitelist and grants access to the restricted Admin Dashboard.
- **Fleet & Logistics Configuration**:
  - **Asset Management**: Tools to add, update, and track the fleet of trucks and reserve vehicles.
  - **Geographical Data**: Management of station types (SOC vs. Hub) and localized province lists in Thailand.
- **Premium Visual Standards**: Implements a consistent design system using the Poppins font and a "premium logistics" aesthetic, ensuring the interface feels professional and efficient.

## 🛠 Technical Pillar
- **Backend**: Unified Firebase architecture (Firestore, Auth, Storage, and Functions) providing a single source of truth for both Web and Mobile.
- **Shared Intelligence**: A centralized document/schema repository ensures that both applications speak the same language when handling trips, drivers, and hubs.

## 🏁 Summary Conclusion
Together, these features create a loop where **Web Admins** assign and monitor tasks, while **Mobile Drivers** execute them with verifiable proof, resulting in a transparent, real-time logistics operation that minimizes errors and maximizes accountability.
