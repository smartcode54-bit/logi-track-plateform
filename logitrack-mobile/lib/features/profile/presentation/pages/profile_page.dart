import 'package:cloud_firestore/cloud_firestore.dart';
import 'package:easy_localization/easy_localization.dart';
import 'package:flutter/material.dart';
import '../../data/repositories/driver_profile_repository.dart';
import '../../../auth/data/repositories/auth_repository.dart';

class ProfilePage extends StatefulWidget {
  const ProfilePage({super.key});

  @override
  State<ProfilePage> createState() => _ProfilePageState();
}

class _ProfilePageState extends State<ProfilePage> {
  final _profileRepository = DriverProfileRepository();
  final _authRepository = AuthRepository();

  Map<String, dynamic>? _profile;
  List<Map<String, dynamic>> _assignmentHistory = [];
  bool _isLoading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _loadProfile();
  }

  Future<void> _loadProfile() async {
    try {
      final user = _authRepository.currentUser;
      if (user != null) {
        final data = await _profileRepository.getProfileByAuthId(user.uid);
        List<Map<String, dynamic>> assignments = [];
        if (data != null && data['id'] != null) {
          try {
            assignments = await _profileRepository.getAssignmentHistory(
              data['id'] as String,
            );
          } catch (_) {}

          // Fallback: if active assignment has no truckType, fetch from driver's currentAssignment.truckId
          {
            final currentTruckId =
                data['currentAssignment']?['truckId'] as String?;
            if (currentTruckId != null && currentTruckId.isNotEmpty) {
              // Find the active assignment and add truckType if missing
              for (int i = 0; i < assignments.length; i++) {
                if (assignments[i]['status'] == 'active' &&
                    assignments[i]['revokedAt'] == null &&
                    (assignments[i]['truckType'] == null ||
                        (assignments[i]['truckType'] as String).isEmpty)) {
                  try {
                    final truckDoc = await FirebaseFirestore.instance
                        .collection('trucks')
                        .doc(currentTruckId)
                        .get();
                    if (truckDoc.exists) {
                      assignments[i]['truckType'] =
                          truckDoc.data()?['type'] as String? ?? '';
                    }
                  } catch (_) {}
                }
              }
            }
          }
        }
        if (mounted) {
          setState(() {
            _profile = data;
            _assignmentHistory = assignments;
            _isLoading = false;
          });
        }
      } else {
        if (mounted) {
          setState(() {
            _error = "user_not_authenticated".tr();
            _isLoading = false;
          });
        }
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _error = e.toString();
          _isLoading = false;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text('driver_profile'.tr()),
        leading: IconButton(
          icon: const Icon(Icons.arrow_back),
          onPressed: () => Navigator.of(context).pop(),
        ),
      ),
      body: SafeArea(top: false, child: _buildBody(context)),
    );
  }

  Widget _buildBody(BuildContext context) {
    if (_isLoading) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24.0),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Icon(
                Icons.error_outline,
                size: 48,
                color: Theme.of(context).colorScheme.error,
              ),
              const SizedBox(height: 16),
              Text('$_error', textAlign: TextAlign.center),
            ],
          ),
        ),
      );
    }
    if (_profile == null) {
      return Center(child: Text('profile_not_found'.tr()));
    }

    final firstName = _profile!['firstName'] ?? '';
    final lastName = _profile!['lastName'] ?? '';
    final displayName = '$firstName $lastName'.trim();
    final mobile = _profile!['mobile'] ?? _profile!['phone'] ?? '-';
    final email =
        _authRepository.currentUser?.email ?? _profile!['email'] ?? '-';
    final idCardNo = _profile!['idCardNo'] ?? _profile!['nationalId'] ?? '-';
    final licenseNumber =
        _profile!['truckLicenseId'] ?? _profile!['licenseNumber'] ?? '-';
    final licenseType = _profile!['licenseType'] ?? '-';
    final employmentType = _profile!['employmentType'] ?? '-';

    return SingleChildScrollView(
      padding: const EdgeInsets.all(16.0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _buildAvatarCard(context, displayName),
          const SizedBox(height: 16),
          _buildSectionTitle(context, 'personal_info'.tr()),
          const SizedBox(height: 8),
          _buildInfoCard(
            context,
            items: [
              _InfoRow(
                icon: Icons.person_outline,
                label: 'name'.tr(),
                value: displayName.isNotEmpty
                    ? displayName
                    : 'profile_driver'.tr(),
              ),
              _InfoRow(
                icon: Icons.phone_outlined,
                label: 'mobile'.tr(),
                value: mobile,
              ),
              _InfoRow(
                icon: Icons.email_outlined,
                label: 'email'.tr(),
                value: email,
              ),
              _InfoRow(
                icon: Icons.badge_outlined,
                label: 'id_card'.tr(),
                value: idCardNo,
              ),
            ],
          ),
          const SizedBox(height: 16),
          _buildSectionTitle(context, 'employment_details'.tr()),
          const SizedBox(height: 8),
          _buildInfoCard(
            context,
            items: [
              _InfoRow(
                icon: Icons.work_outline,
                label: 'employment_type'.tr(),
                value: employmentType,
              ),
              _InfoRow(
                icon: Icons.directions_car_outlined,
                label: 'license_number'.tr(),
                value: licenseNumber,
              ),
              _InfoRow(
                icon: Icons.category_outlined,
                label: 'license_type'.tr(),
                value: licenseType,
              ),
            ],
          ),
          const SizedBox(height: 16),
          _buildSectionTitle(context, 'truck_assignment'.tr()),
          const SizedBox(height: 8),
          _buildAssignmentCard(context),
          const SizedBox(height: 24),
          Card(
            elevation: 2,
            child: ListTile(
              leading: Icon(
                Icons.chat_bubble_outline,
                color: Theme.of(context).colorScheme.primary,
              ),
              title: Text(
                'chat_with_support'.tr(),
                style: const TextStyle(fontWeight: FontWeight.w600),
              ),
              subtitle: Text('chat_support_subtitle'.tr()),
              trailing: const Icon(Icons.chevron_right),
              onTap: () => Navigator.of(context).pushNamed('/chat'),
            ),
          ),
        ],
      ),
    );
  }

  String _formatDate(dynamic value) {
    if (value == null) return '-';
    if (value is DateTime) {
      return '${value.day.toString().padLeft(2, '0')} ${_monthShort(value.month)} ${value.year.toString().substring(2)}';
    }
    return '-';
  }

  String _monthShort(int month) {
    const months = [
      'Jan',
      'Feb',
      'Mar',
      'Apr',
      'May',
      'Jun',
      'Jul',
      'Aug',
      'Sep',
      'Oct',
      'Nov',
      'Dec',
    ];
    return months[month - 1];
  }

  Widget _buildAssignmentCard(BuildContext context) {
    // แสดงแค่คันปัจจุบัน (active, ยังไม่ revoked)
    Map<String, dynamic>? current;
    for (final a in _assignmentHistory) {
      if (a['status'] == 'active' && a['revokedAt'] == null) {
        current = a;
        break;
      }
    }

    if (current == null) {
      return Card(
        elevation: 2,
        child: Padding(
          padding: const EdgeInsets.all(24.0),
          child: Row(
            children: [
              Icon(
                Icons.local_shipping_outlined,
                size: 40,
                color: Theme.of(context).colorScheme.primary.withOpacity(0.6),
              ),
              const SizedBox(width: 16),
              Expanded(
                child: Text(
                  'truck_assignment_no_history'.tr(),
                  style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                    color: Theme.of(
                      context,
                    ).colorScheme.onSurface.withOpacity(0.7),
                  ),
                ),
              ),
            ],
          ),
        ),
      );
    }
    return Card(
      elevation: 2,
      child: ListTile(
        leading: Icon(
          Icons.local_shipping_outlined,
          color: Theme.of(context).colorScheme.primary,
        ),
        title: Text(
          current['truckPlate']?.toString() ?? '-',
          style: const TextStyle(fontWeight: FontWeight.w600),
        ),
        subtitle: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              current['truckModel']?.toString() ?? '',
              style: Theme.of(context).textTheme.bodySmall,
            ),
            if ((current['truckType'] as String?)?.isNotEmpty == true)
              Padding(
                padding: const EdgeInsets.only(top: 2),
                child: Row(
                  children: [
                    Icon(
                      Icons.category_outlined,
                      size: 14,
                      color: Theme.of(context).colorScheme.primary,
                    ),
                    const SizedBox(width: 4),
                    Expanded(
                      child: Text(
                        '${'truck_type'.tr()}: ${current['truckType']}',
                        style: Theme.of(context).textTheme.bodySmall?.copyWith(
                          fontWeight: FontWeight.w500,
                        ),
                        overflow: TextOverflow.ellipsis,
                        maxLines: 1,
                      ),
                    ),
                  ],
                ),
              ),
            Text(
              '${'truck_assignment_start'.tr()}: ${_formatDate(current['createdAt'])}',
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ],
        ),
        trailing: Chip(
          label: Text(
            'truck_assignment_current'.tr(),
            style: const TextStyle(fontSize: 12),
          ),
          backgroundColor: Theme.of(
            context,
          ).colorScheme.primary.withOpacity(0.15),
          padding: EdgeInsets.zero,
          materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
        ),
      ),
    );
  }

  Widget _buildAvatarCard(BuildContext context, String displayName) {
    final initial = displayName.isNotEmpty ? displayName[0].toUpperCase() : 'D';
    return Card(
      elevation: 2,
      child: Padding(
        padding: const EdgeInsets.all(24.0),
        child: Row(
          children: [
            CircleAvatar(
              radius: 40,
              backgroundColor: Theme.of(
                context,
              ).colorScheme.primary.withOpacity(0.2),
              child: Text(
                initial,
                style: TextStyle(
                  fontSize: 32,
                  fontWeight: FontWeight.bold,
                  color: Theme.of(context).colorScheme.primary,
                ),
              ),
            ),
            const SizedBox(width: 20),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    displayName.isNotEmpty
                        ? displayName
                        : 'profile_driver'.tr(),
                    style: Theme.of(context).textTheme.titleLarge?.copyWith(
                      fontWeight: FontWeight.bold,
                    ),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    _authRepository.currentUser?.email ?? '',
                    style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                      color: Theme.of(
                        context,
                      ).colorScheme.onSurface.withOpacity(0.7),
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildSectionTitle(BuildContext context, String title) {
    return Text(
      title,
      style: Theme.of(
        context,
      ).textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w600),
    );
  }

  Widget _buildInfoCard(BuildContext context, {required List<_InfoRow> items}) {
    return Card(
      elevation: 2,
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 8.0),
        child: Column(
          children: items
              .map(
                (row) => ListTile(
                  leading: Icon(
                    row.icon,
                    size: 22,
                    color: Theme.of(context).colorScheme.primary,
                  ),
                  title: Text(
                    row.label,
                    style: Theme.of(context).textTheme.bodySmall?.copyWith(
                      color: Theme.of(
                        context,
                      ).colorScheme.onSurface.withOpacity(0.7),
                    ),
                  ),
                  subtitle: Text(
                    row.value.isEmpty ? '-' : row.value,
                    style: const TextStyle(fontWeight: FontWeight.w500),
                  ),
                ),
              )
              .toList(),
        ),
      ),
    );
  }
}

class _InfoRow {
  final IconData icon;
  final String label;
  final String value;

  _InfoRow({required this.icon, required this.label, required this.value});
}
