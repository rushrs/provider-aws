/*
Copyright 2019 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package role

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	awsiamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane-contrib/provider-aws/apis/iam/v1beta1"
	awsclient "github.com/crossplane-contrib/provider-aws/pkg/clients"
	"github.com/crossplane-contrib/provider-aws/pkg/clients/iam"
	"github.com/crossplane-contrib/provider-aws/pkg/clients/iam/fake"
)

var (
	// an arbitrary managed resource
	unexpectedItem resource.Managed
	roleName       = "some arbitrary name"
	arn            = "some arn"
	description    = "some description"
	policy         = `{
		"Version": "2012-10-17",
		"Statement": [
		  {
			"Effect": "Allow",
			"Principal": {
			  "Service": "eks.amazonaws.com"
			},
			"Action": "sts:AssumeRole"
		  }
		]
	   }`

	errBoom = errors.New("boom")
)

type args struct {
	iam iam.RoleClient
	cr  resource.Managed
}

type roleModifier func(*v1beta1.Role)

func withConditions(c ...xpv1.Condition) roleModifier {
	return func(r *v1beta1.Role) { r.Status.ConditionedStatus.Conditions = c }
}

func withRoleName(s *string) roleModifier {
	return func(r *v1beta1.Role) { meta.SetExternalName(r, *s) }
}

func withArn(s string) roleModifier {
	return func(r *v1beta1.Role) { r.Status.AtProvider.ARN = s }
}

func withPolicy() roleModifier {
	return func(r *v1beta1.Role) {
		p, err := awsclient.CompactAndEscapeJSON(policy)
		if err != nil {
			return
		}
		r.Spec.ForProvider.AssumeRolePolicyDocument = p
	}
}

func withDescription() roleModifier {
	return func(r *v1beta1.Role) {
		r.Spec.ForProvider.Description = aws.String(description)
	}
}

func withTags(tagMaps ...map[string]string) roleModifier {
	var tagList []v1beta1.Tag
	for _, tagMap := range tagMaps {
		for k, v := range tagMap {
			tagList = append(tagList, v1beta1.Tag{Key: k, Value: v})
		}
	}
	return func(r *v1beta1.Role) {
		r.Spec.ForProvider.Tags = tagList
	}
}

func withGroupVersionKind() roleModifier {
	return func(iamRole *v1beta1.Role) {
		iamRole.TypeMeta.SetGroupVersionKind(v1beta1.RoleGroupVersionKind)
	}
}

func role(m ...roleModifier) *v1beta1.Role {
	cr := &v1beta1.Role{}
	for _, f := range m {
		f(cr)
	}

	return cr
}

func TestObserve(t *testing.T) {

	type want struct {
		cr     resource.Managed
		result managed.ExternalObservation
		err    error
	}

	cases := map[string]struct {
		args
		want
	}{
		"VaildInput": {
			args: args{
				iam: &fake.MockRoleClient{
					MockGetRole: func(ctx context.Context, input *awsiam.GetRoleInput, opts []func(*awsiam.Options)) (*awsiam.GetRoleOutput, error) {
						return &awsiam.GetRoleOutput{
							Role: &awsiamtypes.Role{
								Arn: awsclient.String(arn),
							},
						}, nil
					},
				},
				cr: role(withRoleName(&roleName)),
			},
			want: want{
				cr: role(
					withRoleName(&roleName),
					withArn(arn),
					withConditions(xpv1.Available())),
				result: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: true,
					ConnectionDetails: map[string][]byte{
						"arn": []byte(arn),
					},
				},
			},
		},
		"InValidInput": {
			args: args{
				cr: unexpectedItem,
			},
			want: want{
				cr:  unexpectedItem,
				err: errors.New(errUnexpectedObject),
			},
		},
		"ClientError": {
			args: args{
				iam: &fake.MockRoleClient{
					MockGetRole: func(ctx context.Context, input *awsiam.GetRoleInput, opts []func(*awsiam.Options)) (*awsiam.GetRoleOutput, error) {
						return nil, errBoom
					},
				},
				cr: role(withRoleName(&roleName)),
			},
			want: want{
				cr:  role(withRoleName(&roleName)),
				err: awsclient.Wrap(errBoom, errGet),
			},
		},
		"ResourceDoesNotExist": {
			args: args{
				iam: &fake.MockRoleClient{
					MockGetRole: func(ctx context.Context, input *awsiam.GetRoleInput, opts []func(*awsiam.Options)) (*awsiam.GetRoleOutput, error) {
						return nil, &awsiamtypes.NoSuchEntityException{}
					},
				},
				cr: role(),
			},
			want: want{
				cr: role(),
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &external{client: tc.iam}
			o, err := e.Observe(context.Background(), tc.args.cr)

			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
			if diff := cmp.Diff(tc.want.cr, tc.args.cr, test.EquateConditions()); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
			if diff := cmp.Diff(tc.want.result, o); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
		})
	}
}

func TestCreate(t *testing.T) {

	type want struct {
		cr     resource.Managed
		result managed.ExternalCreation
		err    error
	}

	cases := map[string]struct {
		args
		want
	}{
		"VaildInput": {
			args: args{
				iam: &fake.MockRoleClient{
					MockCreateRole: func(ctx context.Context, input *awsiam.CreateRoleInput, opts []func(*awsiam.Options)) (*awsiam.CreateRoleOutput, error) {
						return &awsiam.CreateRoleOutput{}, nil
					},
				},
				cr: role(withRoleName(&roleName)),
			},
			want: want{
				cr: role(
					withRoleName(&roleName),
					withConditions(xpv1.Creating())),
			},
		},
		"InValidInput": {
			args: args{
				cr: unexpectedItem,
			},
			want: want{
				cr:  unexpectedItem,
				err: errors.New(errUnexpectedObject),
			},
		},
		"ClientError": {
			args: args{
				iam: &fake.MockRoleClient{
					MockCreateRole: func(ctx context.Context, input *awsiam.CreateRoleInput, opts []func(*awsiam.Options)) (*awsiam.CreateRoleOutput, error) {
						return nil, errBoom
					},
				},
				cr: role(),
			},
			want: want{
				cr:  role(withConditions(xpv1.Creating())),
				err: awsclient.Wrap(errBoom, errCreate),
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &external{client: tc.iam}
			o, err := e.Create(context.Background(), tc.args.cr)

			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
			if diff := cmp.Diff(tc.want.cr, tc.args.cr, test.EquateConditions()); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
			if diff := cmp.Diff(tc.want.result, o); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
		})
	}
}

func TestUpdate(t *testing.T) {

	type want struct {
		cr     resource.Managed
		result managed.ExternalUpdate
		err    error
	}

	cases := map[string]struct {
		args
		want
	}{
		"VaildInput": {
			args: args{
				iam: &fake.MockRoleClient{
					MockGetRole: func(ctx context.Context, input *awsiam.GetRoleInput, opts []func(*awsiam.Options)) (*awsiam.GetRoleOutput, error) {
						return &awsiam.GetRoleOutput{
							Role: &awsiamtypes.Role{},
						}, nil
					},
					MockUpdateRole: func(ctx context.Context, input *awsiam.UpdateRoleInput, opts []func(*awsiam.Options)) (*awsiam.UpdateRoleOutput, error) {
						return &awsiam.UpdateRoleOutput{}, nil
					},
				},
				cr: role(withRoleName(&roleName)),
			},
			want: want{
				cr: role(withRoleName(&roleName)),
			},
		},
		"InValidInput": {
			args: args{
				cr: unexpectedItem,
			},
			want: want{
				cr:  unexpectedItem,
				err: errors.New(errUnexpectedObject),
			},
		},
		"ClientUpdateRoleError": {
			args: args{
				iam: &fake.MockRoleClient{
					MockGetRole: func(ctx context.Context, input *awsiam.GetRoleInput, opts []func(*awsiam.Options)) (*awsiam.GetRoleOutput, error) {
						return &awsiam.GetRoleOutput{
							Role: &awsiamtypes.Role{},
						}, nil
					},
					MockUpdateRole: func(ctx context.Context, input *awsiam.UpdateRoleInput, opts []func(*awsiam.Options)) (*awsiam.UpdateRoleOutput, error) {
						return nil, errBoom
					},
				},
				cr: role(withDescription()),
			},
			want: want{
				cr:  role(withDescription()),
				err: awsclient.Wrap(errBoom, errUpdate),
			},
		},
		"ClientUpdatePolicyError": {
			args: args{
				iam: &fake.MockRoleClient{
					MockGetRole: func(ctx context.Context, input *awsiam.GetRoleInput, opts []func(*awsiam.Options)) (*awsiam.GetRoleOutput, error) {
						return &awsiam.GetRoleOutput{
							Role: &awsiamtypes.Role{},
						}, nil
					},
					MockUpdateRole: func(ctx context.Context, input *awsiam.UpdateRoleInput, opts []func(*awsiam.Options)) (*awsiam.UpdateRoleOutput, error) {
						return &awsiam.UpdateRoleOutput{}, nil
					},
					MockUpdateAssumeRolePolicy: func(ctx context.Context, input *awsiam.UpdateAssumeRolePolicyInput, opts []func(*awsiam.Options)) (*awsiam.UpdateAssumeRolePolicyOutput, error) {
						return nil, errBoom
					},
				},
				cr: role(withPolicy()),
			},
			want: want{
				cr:  role(withPolicy()),
				err: awsclient.Wrap(errBoom, errUpdate),
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &external{client: tc.iam}
			o, err := e.Update(context.Background(), tc.args.cr)

			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
			if diff := cmp.Diff(tc.want.cr, tc.args.cr, test.EquateConditions()); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
			if diff := cmp.Diff(tc.want.result, o); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
		})
	}
}

func TestDelete(t *testing.T) {

	type want struct {
		cr  resource.Managed
		err error
	}

	cases := map[string]struct {
		args
		want
	}{
		"VaildInput": {
			args: args{
				iam: &fake.MockRoleClient{
					MockDeleteRole: func(ctx context.Context, input *awsiam.DeleteRoleInput, opts []func(*awsiam.Options)) (*awsiam.DeleteRoleOutput, error) {
						return &awsiam.DeleteRoleOutput{}, nil
					},
				},
				cr: role(withRoleName(&roleName)),
			},
			want: want{
				cr: role(withRoleName(&roleName),
					withConditions(xpv1.Deleting())),
			},
		},
		"InValidInput": {
			args: args{
				cr: unexpectedItem,
			},
			want: want{
				cr:  unexpectedItem,
				err: errors.New(errUnexpectedObject),
			},
		},
		"ClientError": {
			args: args{
				iam: &fake.MockRoleClient{
					MockDeleteRole: func(ctx context.Context, input *awsiam.DeleteRoleInput, opts []func(*awsiam.Options)) (*awsiam.DeleteRoleOutput, error) {
						return nil, errBoom
					},
				},
				cr: role(),
			},
			want: want{
				cr:  role(withConditions(xpv1.Deleting())),
				err: awsclient.Wrap(errBoom, errDelete),
			},
		},
		"ResourceDoesNotExist": {
			args: args{
				iam: &fake.MockRoleClient{
					MockDeleteRole: func(ctx context.Context, input *awsiam.DeleteRoleInput, opts []func(*awsiam.Options)) (*awsiam.DeleteRoleOutput, error) {
						return nil, &awsiamtypes.NoSuchEntityException{}
					},
				},
				cr: role(),
			},
			want: want{
				cr: role(withConditions(xpv1.Deleting())),
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &external{client: tc.iam}
			err := e.Delete(context.Background(), tc.args.cr)

			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
			if diff := cmp.Diff(tc.want.cr, tc.args.cr, test.EquateConditions()); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
		})
	}

}

func TestInitialize(t *testing.T) {
	type args struct {
		cr   resource.Managed
		kube client.Client
	}
	type want struct {
		cr  *v1beta1.Role
		err error
	}

	cases := map[string]struct {
		args
		want
	}{
		"Unexpected": {
			args: args{
				cr:   unexpectedItem,
				kube: &test.MockClient{MockUpdate: test.NewMockUpdateFn(nil)},
			},
			want: want{
				err: errors.New(errUnexpectedObject),
			},
		},
		"Successful": {
			args: args{
				cr:   role(withTags(map[string]string{"foo": "bar"})),
				kube: &test.MockClient{MockUpdate: test.NewMockUpdateFn(nil)},
			},
			want: want{
				cr: role(withTags(resource.GetExternalTags(role()), map[string]string{"foo": "bar"})),
			},
		},
		"DefaultTags": {
			args: args{
				cr:   role(withTags(map[string]string{"foo": "bar"}), withGroupVersionKind()),
				kube: &test.MockClient{MockUpdate: test.NewMockUpdateFn(nil)},
			},
			want: want{
				cr: role(withTags(resource.GetExternalTags(role(withGroupVersionKind())), map[string]string{"foo": "bar"}), withGroupVersionKind()),
			},
		},
		"UpdateDefaultTags": {
			args: args{
				cr: role(
					withRoleName(&roleName),
					withGroupVersionKind(),
					withTags(map[string]string{resource.ExternalResourceTagKeyKind: "bar"})),
				kube: &test.MockClient{MockUpdate: test.NewMockUpdateFn(nil)},
			},
			want: want{
				cr: role(
					withRoleName(&roleName),
					withGroupVersionKind(),
					withTags(resource.GetExternalTags(role(withRoleName(&roleName), withGroupVersionKind())))),
			},
		},
		"NoChange": {
			args: args{
				cr: role(
					withRoleName(&roleName),
					withGroupVersionKind(),
					withTags(resource.GetExternalTags(role(withRoleName(&roleName), withGroupVersionKind())))),
				kube: &test.MockClient{MockUpdate: test.NewMockUpdateFn(nil)},
			},
			want: want{
				cr: role(
					withRoleName(&roleName),
					withGroupVersionKind(),
					withTags(resource.GetExternalTags(role(withRoleName(&roleName), withGroupVersionKind())))),
			},
		},
		"UpdateFailed": {
			args: args{
				cr:   role(),
				kube: &test.MockClient{MockUpdate: test.NewMockUpdateFn(errBoom)},
			},
			want: want{
				err: errors.Wrap(errBoom, errKubeUpdateFailed),
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &tagger{kube: tc.kube}
			err := e.Initialize(context.Background(), tc.args.cr)

			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
			if diff := cmp.Diff(tc.want.cr, tc.args.cr, cmpopts.SortSlices(func(a, b v1beta1.Tag) bool { return a.Key > b.Key })); err == nil && diff != "" {
				t.Errorf("r: -want, +got:\n%s", diff)
			}
		})
	}
}
