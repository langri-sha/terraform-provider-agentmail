# A list is imported by its scope, direction and list type.
terraform import 'agentmail_list.support_allow["receive"]' inbox/support@agentmail.to/receive/allow
terraform import agentmail_list.pod_block pod/pod_123/receive/block
terraform import agentmail_list.no_competitors org/send/block
